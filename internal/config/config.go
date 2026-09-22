package config

import (
	"encoding/json"
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
	MaxConcurrency     int    `toml:"max_concurrency" json:"max_concurrency"`
	Command            any    `toml:"command" json:"command,omitempty"`
	Protocol           string `toml:"protocol" json:"protocol"`
	Stdin              bool   `toml:"stdin" json:"stdin"`
	HealthCommand      any    `toml:"health_command" json:"health_command,omitempty"`
	Transport          string `toml:"transport" json:"transport,omitempty"`
	CLI                CLI    `toml:"cli" json:"cli,omitempty"`
	API                API    `toml:"api" json:"api,omitempty"`
	IdleTimeoutEnabled *bool  `toml:"idle_timeout_enabled" json:"idle_timeout_enabled,omitempty"`
}

type CLI struct {
	Command       []string `toml:"command" json:"command,omitempty"`
	StatusCommand []string `toml:"status_command" json:"status_command,omitempty"`
	LoginCommand  []string `toml:"login_command" json:"login_command,omitempty"`
}

type API struct {
	BaseURL   string `toml:"base_url" json:"base_url,omitempty"`
	Model     string `toml:"model" json:"model,omitempty"`
	WireAPI   string `toml:"wire_api" json:"wire_api,omitempty"`
	APIKeyEnv string `toml:"api_key_env" json:"api_key_env,omitempty"`
}

type Scheduler struct {
	Strategy              string   `toml:"strategy" json:"strategy"`
	Order                 []string `toml:"order" json:"order"`
	SessionMaxConcurrency int      `toml:"session_max_concurrency" json:"session_max_concurrency"`
	MachineMaxConcurrency int      `toml:"machine_max_concurrency" json:"machine_max_concurrency"`
}

type Laya struct {
	Mode           string     `toml:"mode" json:"mode"`
	Runner         []string   `toml:"runner" json:"runner,omitempty"`
	TimeoutSeconds int        `toml:"timeout_seconds" json:"timeout_seconds"`
	Supervisor     Supervisor `toml:"supervisor" json:"supervisor"`
}

type Supervisor struct {
	Mode             string `toml:"mode" json:"mode"`
	HeartbeatSeconds int    `toml:"heartbeat_seconds" json:"heartbeat_seconds"`
	StaleSeconds     int    `toml:"stale_seconds" json:"stale_seconds"`
	ExtensionSeconds int    `toml:"extension_seconds" json:"extension_seconds"`
	MaxExtensions    int    `toml:"max_extensions" json:"max_extensions"`
	MaxRetries       int    `toml:"max_retries" json:"max_retries"`
}

type Config struct {
	Scheduler Scheduler          `toml:"scheduler" json:"scheduler"`
	Backend   map[string]Backend `toml:"backend" json:"backend"`
	Timeouts  Timeouts           `toml:"timeouts" json:"timeouts"`
	Laya      Laya               `toml:"laya" json:"laya"`
}

func Defaults() Config {
	return Config{
		Scheduler: Scheduler{Strategy: "round_robin", Order: []string{"agy", "qwen", "claude"}, SessionMaxConcurrency: 10, MachineMaxConcurrency: 13},
		Backend: map[string]Backend{
			"agy":    {MaxConcurrency: 10, Protocol: "agy"},
			"qwen":   {MaxConcurrency: 1, Protocol: "qwen", Stdin: true, Transport: "auto"},
			"claude": {MaxConcurrency: 2, Protocol: "claude"},
		},
		Timeouts: Timeouts{MaxSeconds: 14400, Defaults: map[string]int{"inspect": 900, "implement": 3600}},
		Laya:     Laya{Mode: "shadow", TimeoutSeconds: 10, Supervisor: Supervisor{Mode: "shadow", HeartbeatSeconds: 5, StaleSeconds: 15, ExtensionSeconds: 300, MaxExtensions: 2, MaxRetries: 1}},
	}
}

func Load(path string) (Config, error) {
	if path == "" {
		return LoadFor("")
	}
	return loadFiles([]string{path})
}

func LoadFor(workspace string) (Config, error) {
	if explicit := os.Getenv("VIOLIN_CONFIG"); explicit != "" {
		return loadFiles([]string{explicit})
	}
	home, _ := os.UserHomeDir()
	// Read the legacy path first so the public Violin config wins when both
	// files exist during migration.
	paths := []string{filepath.Join(home, ".config", "violin-agents", "config.toml"), filepath.Join(home, ".config", "violin", "config.toml")}
	if workspace != "" {
		paths = append(paths, filepath.Join(workspace, ".violin", "config.toml"))
	}
	return loadFiles(paths)
}

func loadFiles(paths []string) (Config, error) {
	c := Defaults()
	for _, path := range paths {
		if data, err := os.ReadFile(path); err == nil {
			if _, err = toml.Decode(string(data), &c); err != nil {
				return c, err
			}
		} else if !os.IsNotExist(err) {
			return c, err
		}
	}
	applyBackendEnv(&c)
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
	if value := os.Getenv("VIOLIN_LAYA_MODE"); value != "" {
		c.Laya.Mode = value
	}
	if value := os.Getenv("VIOLIN_LAYA_RUNNER"); value != "" {
		var runner []string
		if json.Unmarshal([]byte(value), &runner) == nil {
			c.Laya.Runner = runner
		}
	}
	if value := os.Getenv("VIOLIN_LAYA_TIMEOUT_SECONDS"); value != "" {
		c.Laya.TimeoutSeconds = atoi(value, c.Laya.TimeoutSeconds)
	}
	if c.Laya.Mode != "shadow" && c.Laya.Mode != "advisory" && c.Laya.Mode != "active" {
		return c, os.ErrInvalid
	}
	if c.Laya.TimeoutSeconds < 1 {
		return c, os.ErrInvalid
	}
	if c.Laya.Supervisor.Mode == "" {
		c.Laya.Supervisor = Defaults().Laya.Supervisor
	}
	if c.Laya.Supervisor.Mode != "shadow" && c.Laya.Supervisor.Mode != "advisory" && c.Laya.Supervisor.Mode != "active" {
		return c, os.ErrInvalid
	}
	if c.Laya.Supervisor.HeartbeatSeconds < 1 || c.Laya.Supervisor.StaleSeconds < c.Laya.Supervisor.HeartbeatSeconds || c.Laya.Supervisor.ExtensionSeconds < 1 || c.Laya.Supervisor.MaxExtensions < 0 || c.Laya.Supervisor.MaxRetries < 0 || c.Laya.Supervisor.MaxRetries > 1 {
		return c, os.ErrInvalid
	}
	if c.Timeouts.Defaults["inspect"] > c.Timeouts.MaxSeconds || c.Timeouts.Defaults["implement"] > c.Timeouts.MaxSeconds {
		return c, os.ErrInvalid
	}
	return c, nil
}

func applyBackendEnv(c *Config) {
	for _, name := range []string{"qwen", "agy", "claude"} {
		backend := c.Backend[name]
		prefix := "VIOLIN_" + strings.ToUpper(name) + "_"
		if value := os.Getenv(prefix + "TRANSPORT"); value != "" {
			backend.Transport = value
		}
		if value := os.Getenv(prefix + "API_BASE_URL"); value != "" {
			backend.API.BaseURL = value
		}
		if value := os.Getenv(prefix + "MODEL"); value != "" {
			backend.API.Model = value
		}
		if value := os.Getenv(prefix + "API_KEY_ENV"); value != "" {
			backend.API.APIKeyEnv = value
		}
		if value := os.Getenv(prefix + "CLI_COMMAND"); value != "" {
			var command []string
			if json.Unmarshal([]byte(value), &command) == nil && len(command) > 0 {
				backend.CLI.Command = command
				backend.Transport = "cli"
			}
		}
		c.Backend[name] = backend
	}
}

func IdleTimeoutEnabled(name string, backend Backend) bool {
	if backend.IdleTimeoutEnabled != nil {
		return *backend.IdleTimeoutEnabled
	}
	builtIn := backend.Transport == "api" || (backend.Transport == "auto" && len(backend.CLI.Command) == 0 && backend.Command == nil)
	if builtIn && (name == "qwen" || name == "agy") {
		return false
	}
	return true
}

func atoi(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
