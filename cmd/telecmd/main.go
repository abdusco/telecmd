package main

import (
	"context"
	"fmt"
	"github.com/abdusco/telecmd/internal/telecmd"
	"github.com/abdusco/telecmd/internal/version"
	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"os"
	"os/signal"
	"syscall"
)

type cliArgs struct {
	Version    kong.VersionFlag `help:"Show version"`
	ConfigPath string           `arg:"" type:"existingfile" help:"Path to config file"`
	Token      string           `env:"TELEGRAM_BOT_TOKEN" required:"" help:"Telegram bot token"`
	Debug      bool             `env:"DEBUG" default:"false" help:"Enable debug logging"`
}

func main() {
	var args cliArgs
	kong.Parse(&args, kong.Vars{"version": version.GitVersion().String()})

	level := zerolog.InfoLevel
	if args.Debug {
		level = zerolog.DebugLevel
	}
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(level)

	config, err := loadConfig(args.ConfigPath)
	if err != nil {
		log.Fatal().Err(err).Msg("error loading config")
	}

	config.Debug = args.Debug
	config.BotToken = args.Token

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	tc := telecmd.New(config)

	if err := tc.Run(ctx); err != nil {
		log.Fatal().Err(err).Msg("exit with error")
	}

	log.Info().Msg("shutting down")
}

func loadConfig(configPath string) (telecmd.Config, error) {
	if configPath == "" {
		return telecmd.Config{}, fmt.Errorf("config not specified")
	}

	var config telecmd.Config
	b, err := os.ReadFile(configPath)
	if err != nil {
		return telecmd.Config{}, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(b, &config); err != nil {
		return telecmd.Config{}, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := config.Validate(); err != nil {
		return telecmd.Config{}, fmt.Errorf("invalid config: %w", err)
	}

	return config, nil
}
