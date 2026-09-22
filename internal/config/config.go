package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type Timeouts struct {
	MaxSeconds int            `toml:"max_seconds" json:"max_seconds"`
	Defaults   map[string]int `toml:"defaults" json:"defaults"`
}

type Backend struct {
	MaxConcurrency int    `toml:"max_concurrency" json:"max_concurrency"`
	Command        any    `toml:"command" json:"command,omitempty"`
	Protocol       string `toml:"protocol" json:"protocol"`
	Stdin          bool   `toml:"stdin" json:"stdin"`
	HealthCommand  any    `toml:"health_command" json:"health_command,omitempty"`
}

type Scheduler struct {
	Strategy              string   `toml:"strategy" json:"strategy"`
	Order                 []string `toml:"order" json:"order"`
	SessionMaxConcurrency int      `toml:"session_max_concurrency" json:"session_max_concurrency"`
	MachineMaxConcurrency int      `toml:"machine_max_concurrency" json:"machine_max_concurrency"`
}

type Config struct {
	Scheduler Scheduler          `toml:"scheduler" json:"scheduler"`
	Backend   map[string]Backend `toml:"backend" json:"backend"`
	Timeouts  Timeouts           `toml:"timeouts" json:"timeouts"`
}

func Defaults() Config {
	return Config{
		Scheduler: Scheduler{Strategy: "round_robin", Order: []string{"agy", "qwen", "claude"}, SessionMaxConcurrency: 10, MachineMaxConcurrency: 13},
		Backend: map[string]Backend{
			"agy":    {MaxConcurrency: 10, Protocol: "agy"},
			"qwen":   {MaxConcurrency: 1, Protocol: "qwen", Stdin: true},
			"claude": {MaxConcurrency: 2, Protocol: "claude"},
		},
		Timeouts: Timeouts{MaxSeconds: 14400, Defaults: map[string]int{"inspect": 900, "implement": 3600}},
	}
}

func Load(path string) (Config, error) {
	c := Defaults()
	if path == "" {
		path = os.Getenv("VIOLIN_CONFIG")
		if path == "" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, ".config", "violin-agents", "config.toml")
		}
	}
	if data, err := os.ReadFile(path); err == nil {
		if _, err = toml.Decode(string(data), &c); err != nil {
			return c, err
		}
	} else if !os.IsNotExist(err) {
		return c, err
	}
	if value := os.Getenv("VIOLIN_BACKEND_ORDER"); value != "" {
		c.Scheduler.Order = strings.Split(value, ",")
	}
	if value := os.Getenv("VIOLIN_SESSION_MAX_CONCURRENCY"); value != "" {
		c.Scheduler.SessionMaxConcurrency = atoi(value, c.Scheduler.SessionMaxConcurrency)
	}
	if value := os.Getenv("VIOLIN_MACHINE_MAX_CONCURRENCY"); value != "" {
		c.Scheduler.MachineMaxConcurrency = atoi(value, c.Scheduler.MachineMaxConcurrency)
	}
	if value := os.Getenv("VIOLIN_TIMEOUT_MAX_SECONDS"); value != "" {
		c.Timeouts.MaxSeconds = atoi(value, c.Timeouts.MaxSeconds)
	}
	for _, mode := range []string{"inspect", "implement"} {
		if value := os.Getenv("VIOLIN_" + strings.ToUpper(mode) + "_TIMEOUT_SECONDS"); value != "" {
			c.Timeouts.Defaults[mode] = atoi(value, c.Timeouts.Defaults[mode])
		}
	}
	if c.Timeouts.Defaults["inspect"] > c.Timeouts.MaxSeconds || c.Timeouts.Defaults["implement"] > c.Timeouts.MaxSeconds {
		return c, os.ErrInvalid
	}
	return c, nil
}

func atoi(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
