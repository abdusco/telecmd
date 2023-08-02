package telecmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
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
	UseStdin         bool     `yaml:"useStdin"`
	Environment      []string `yaml:"env"`
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
	BotToken       string
	Debug          bool
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
