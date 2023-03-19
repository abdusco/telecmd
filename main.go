package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/ghodss/yaml"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sourcegraph/conc/pool"
	"golang.org/x/exp/slices"

	"github.com/abdusco/telecmd/version"
)

type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var res any
	if err := json.Unmarshal(b, &res); err != nil {
		return err
	}

	switch v := res.(type) {
	case string:
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("failed to parse as duration: %w", err)
		}
		*d = Duration(parsed)
	default:
		return fmt.Errorf("not a duration")
	}
	return nil
}

type Rule struct {
	Name             string   `yaml:"name"`
	Pattern          string   `yaml:"pattern"`
	WorkingDirectory string   `yaml:"workingDir"`
	Command          []string `yaml:"command"`
}

func (r Rule) Validate() error {
	_, err := regexp.Compile(r.Pattern)
	if err != nil {
		return fmt.Errorf("invalid regex: %w", err)
	}
	if len(r.Command) == 0 {
		return fmt.Errorf("invalid command")
	}
	return nil
}

type Config struct {
	Rules          []Rule `yaml:"rules"`
	CommandTimeout string `yaml:"commandTimeout"`
}

func (c Config) CommandTimeoutDuration() time.Duration {
	timeout := time.Minute
	if parsed, err := time.ParseDuration(c.CommandTimeout); err == nil {
		timeout = parsed
	}
	return timeout
}

func (c Config) Validate() error {
	if len(c.Rules) == 0 {
		return fmt.Errorf("rule list cannot be empty")
	}
	for i, rule := range c.Rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("invalid rule %d: %w", i, err)
		}
	}
	return nil
}

func main() {
	var cliArgs struct {
		Version    kong.VersionFlag `help:"Show version"`
		ConfigPath string           `arg:"" type:"existingfile" help:"Path to config file"`
		Token      string           `env:"TELEGRAM_BOT_TOKEN" required:"" help:"Telegram bot token"`
		Debug      bool             `env:"DEBUG" default:"false" help:"Enable debug logging"`
	}
	kong.Parse(&cliArgs, kong.Vars{"version": version.GitVersion().String()})
	level := zerolog.InfoLevel
	if cliArgs.Debug {
		level = zerolog.DebugLevel
	}
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(level)

	if cliArgs.ConfigPath == "" {
		log.Fatal().Msg("config not specified")
	}

	var config Config
	b, err := os.ReadFile(cliArgs.ConfigPath)
	if err != nil {
		log.Fatal().
			Err(err).
			Str("path", cliArgs.ConfigPath).
			Msg("cannot read config file")
	}
	if err := yaml.Unmarshal(b, &config); err != nil {
		log.Fatal().
			Err(err).
			Str("path", cliArgs.ConfigPath).
			Msg("cannot parse config file")
	}

	if err := config.Validate(); err != nil {
		log.Fatal().Err(err).Msg("invalid config")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	bot, err := tgbotapi.NewBotAPI(cliArgs.Token)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot create bot")
	}
	bot.Debug = cliArgs.Debug
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	u.AllowedUpdates = []string{"message", "callback_query"}
	updatesChan := bot.GetUpdatesChan(u)

	procPool := pool.New().WithMaxGoroutines(4)

	log.Info().Msg("listening")

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("shutting down")
			return
		case update := <-updatesChan:
			procPool.Go(func() {
				chatMessage := update.Message.Text

				log.Info().
					Str("user", update.Message.From.FirstName).
					Str("chat_message", chatMessage).
					Msg("got message")

				var matchedRule *Rule
				for _, rule := range config.Rules {
					ok, _ := regexp.MatchString(rule.Pattern, chatMessage)
					if ok {
						matchedRule = &rule
					}
				}
				if matchedRule == nil {
					log.Debug().Msg("no matching rule")
					return
				}

				log.Debug().Interface("rule", matchedRule).Msg("matched rule")

				timeout := config.CommandTimeoutDuration()
				cmdContext, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()

				args := slices.Clone(matchedRule.Command)
				args = append(args, "--", update.Message.Text)

				exe := args[0]
				cmdArgs := args[1:]
				log.Debug().Str("command", exe).Strs("args", cmdArgs).Msg("running command")

				cmd := exec.CommandContext(cmdContext, exe, cmdArgs...)
				if matchedRule.WorkingDirectory != "" {
					cmd.Dir = matchedRule.WorkingDirectory
				}
				cmd.Env = os.Environ()
				cmd.Env = append(cmd.Env, envsFromUpdate(update)...)

				var reply string
				if out, err := cmd.Output(); err != nil {
					log.Debug().Str("command", exe).Strs("args", cmdArgs).Err(err).Msg("command finished with error")
					var exitErr *exec.ExitError
					if cmdContext.Err() == context.DeadlineExceeded {
						reply = fmt.Sprintf("error: command timed out after %v", timeout)
					} else if errors.As(err, &exitErr) {
						reply = fmt.Sprintf("error: command exited with code=%d\n\n%v", exitErr.ExitCode(), string(exitErr.Stderr))
					}
				} else {
					reply = string(out)
				}

				if reply == "" {
					return
				}

				if update.Message == nil {
					return
				}

				m, err := chattableFromStdout(update.Message.Chat.ID, reply)
				if err != nil {
					log.Error().Err(err).Msg("cannot parse stdout")
					return
				}
				switch v := m.(type) {
				case *tgbotapi.MessageConfig:
					v.ReplyToMessageID = update.Message.MessageID
				}

				if _, err = bot.Send(m); err != nil {
					log.Error().Err(err).Msg("failed to reply")
					return
				}
			})
		}
	}
}

func chattableFromStdout(chatID int64, output string) (tgbotapi.Chattable, error) {
	if !strings.HasPrefix(strings.TrimSpace(output), "{") {
		// not json

		m := tgbotapi.NewMessage(chatID, output)
		m.ReplyMarkup = tgbotapi.NewRemoveKeyboard(false)

		return m, nil
	}

	var maybeMessage struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(output), &maybeMessage); err == nil {
		m := tgbotapi.NewMessage(chatID, maybeMessage.Message)
		m.ReplyMarkup = tgbotapi.NewRemoveKeyboard(false)
		return m, nil
	}

	return nil, fmt.Errorf("unknown output format")
}

func envsFromUpdate(update tgbotapi.Update) []string {
	var envs []string

	if m := update.Message; m != nil {
		if m.Chat != nil {
			envs = append(envs, fmt.Sprintf("TELEGRAM_CHAT_ID=%d", m.Chat.ID))
		}
		if m.From != nil {
			envs = append(envs, fmt.Sprintf("TELEGRAM_FROM_USER_ID=%d", m.From.ID))
		}
		if m.ReplyToMessage != nil {
			envs = append(
				envs,
				fmt.Sprintf("TELEGRAM_REPLY_TO_MESSAGE_ID=%d", m.ReplyToMessage.MessageID),
				fmt.Sprintf("TELEGRAM_REPLY_TO_MESSAGE_TEXT=%s", m.ReplyToMessage.Text),
			)
		}
	}

	return envs
}
