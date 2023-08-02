package telecmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog/log"
	"github.com/sourcegraph/conc/pool"
	"golang.org/x/exp/slices"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type Telecmd struct {
	config Config
}

func New(config Config) Telecmd {
	return Telecmd{config: config}
}

func (t Telecmd) Run(ctx context.Context) error {
	bot, err := tgbotapi.NewBotAPI(t.config.BotToken)
	if err != nil {
		return fmt.Errorf("failed to create bot: %w", err)
	}
	bot.Debug = t.config.Debug

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	u.AllowedUpdates = []string{"message", "callback_query"}
	updatesChan := bot.GetUpdatesChan(u)

	procPool := pool.New().WithMaxGoroutines(4)

	log.Info().Msg("listening")

	for {
		select {
		case <-ctx.Done():
			return nil
		case update := <-updatesChan:
			procPool.Go(func() {
				if update.Message == nil {
					return
				}

				log.Info().
					Str("user", update.Message.From.FirstName).
					Str("chat_message", update.Message.Text).
					Msg("got message")

				rule, ok := t.ruleFromMessage(update.Message)
				if !ok {
					log.Debug().Msg("no matching rule")
					return
				}

				log.Debug().Interface("rule", rule).Msg("matched rule")

				timeout := t.config.CommandTimeoutDuration()
				cmdContext, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()

				cmd, err := commandFromMessage(cmdContext, rule, update.Message)
				if err != nil {
					log.Error().Err(err).Msg("cannot parse command")
					return
				}

				output, err := t.runCommand(cmdContext, cmd)
				if err != nil {
					log.Debug().Str("command", cmd.Path).Strs("args", cmd.Args).Err(err).Msg("command finished with error")
					output = err.Error()
				}

				if output == "" {
					return
				}

				m, err := chattableFromStdout(update.Message.Chat.ID, output)
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

func (t Telecmd) ruleFromMessage(message *tgbotapi.Message) (Rule, bool) {
	for _, rule := range t.config.Rules {
		ok, _ := regexp.MatchString(rule.Pattern, message.Text)
		if ok {
			return rule, true
		}
	}
	return Rule{}, false
}

func (t Telecmd) runCommand(ctx context.Context, cmd *exec.Cmd) (string, error) {
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("command took too long to finish")
		} else if errors.As(err, &exitErr) {
			return "", fmt.Errorf("command exited with code=%d\n\n%v", exitErr.ExitCode(), string(exitErr.Stderr))
		}
	}

	return string(out), nil
}

func commandFromMessage(ctx context.Context, rule Rule, message *tgbotapi.Message) (*exec.Cmd, error) {
	var stdin io.Reader
	args := slices.Clone(rule.Command)
	if rule.UseStdin {
		stdin = strings.NewReader(message.Text)
	} else {
		args = append(args, "--", message.Text)
	}

	exe := args[0]
	cmdArgs := args[1:]
	log.Debug().Str("command", exe).Strs("args", cmdArgs).Msg("running command")

	cmd := exec.CommandContext(ctx, exe, cmdArgs...)
	if rule.WorkingDirectory != "" {
		cmd.Dir = rule.WorkingDirectory
	}
	cmd.Stdin = stdin
	env := os.Environ()
	env = append(env, rule.Environment...)
	env = append(env, envsFromUpdate(message)...)
	cmd.Env = env

	return cmd, nil
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

func envsFromUpdate(message *tgbotapi.Message) []string {
	if message == nil {
		return nil
	}

	var envs []string

	if message.Chat != nil {
		envs = append(envs, fmt.Sprintf("TELEGRAM_CHAT_ID=%d", message.Chat.ID))
	}

	if message.From != nil {
		envs = append(envs, fmt.Sprintf("TELEGRAM_FROM_USER_ID=%d", message.From.ID))
	}

	if message.ReplyToMessage != nil {
		envs = append(
			envs,
			fmt.Sprintf("TELEGRAM_REPLY_TO_MESSAGE_ID=%d", message.ReplyToMessage.MessageID),
			fmt.Sprintf("TELEGRAM_REPLY_TO_MESSAGE_TEXT=%s", message.ReplyToMessage.Text),
		)
	}

	return envs
}
