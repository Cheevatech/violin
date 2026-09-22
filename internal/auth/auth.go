package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
)

type ProviderStatus struct {
	Provider      string `json:"provider"`
	Authenticated bool   `json:"authenticated"`
	Source        string `json:"source"`
	Action        string `json:"action,omitempty"`
	Message       string `json:"message,omitempty"`
}

type CommandRunner func(context.Context, string, ...string) ([]byte, error)

type Manager struct {
	Store  credentials.Store
	Runner CommandRunner
}

func NewManager(store credentials.Store) Manager {
	return Manager{Store: store, Runner: runCommand}
}

func (m Manager) Status(ctx context.Context, provider string) (ProviderStatus, error) {
	if provider == "all" {
		return ProviderStatus{Provider: "all", Message: "query providers individually"}, nil
	}
	switch provider {
	case "claude":
		return m.cliStatus(ctx, "claude", "claude", "auth", "status", "--json")
	case "qwen":
		return m.cliStatus(ctx, "qwen", "codex", "login", "status")
	case "agy":
		if _, err := m.Store.Lookup(ctx, "VIOLIN_AGY_API_KEY", "violin/agy"); err == nil {
			return ProviderStatus{Provider: "agy", Authenticated: true, Source: "environment_or_keychain"}, nil
		}
		return ProviderStatus{Provider: "agy", Source: "environment_or_keychain", Action: "configure", Message: "set VIOLIN_AGY_API_KEY or store service violin/agy in the OS keychain"}, nil
	default:
		return ProviderStatus{}, fmt.Errorf("unknown provider %q", provider)
	}
}

func (m Manager) StatusWithBackend(ctx context.Context, provider string, backend config.Backend) (ProviderStatus, error) {
	transport := backend.Transport
	if transport == "" {
		transport = "auto"
	}
	if transport == "api" {
		return m.apiStatus(ctx, provider, backend)
	}
	if len(backend.CLI.StatusCommand) > 0 {
		return m.cliStatusArgs(ctx, provider, backend.CLI.StatusCommand)
	}
	if transport == "cli" {
		return ProviderStatus{Provider: provider, Source: "cli", Action: "configure", Message: "configure backend.cli.status_command"}, nil
	}
	if backend.API.BaseURL != "" || backend.API.APIKeyEnv != "" {
		return m.apiStatus(ctx, provider, backend)
	}
	return ProviderStatus{Provider: provider, Source: "unconfigured", Action: "configure", Message: "configure backend.transport and CLI or API settings"}, nil
}

func (m Manager) apiStatus(ctx context.Context, provider string, backend config.Backend) (ProviderStatus, error) {
	if strings.TrimSpace(backend.API.BaseURL) == "" {
		return ProviderStatus{Provider: provider, Source: "api", Action: "configure", Message: "configure backend.api.base_url"}, nil
	}
	envName := backend.API.APIKeyEnv
	if envName == "" {
		envName = "VIOLIN_" + strings.ToUpper(provider) + "_API_KEY"
	}
	if _, err := m.Store.Lookup(ctx, envName, "violin/"+provider+"/api-key"); err == nil {
		return ProviderStatus{Provider: provider, Authenticated: true, Source: "environment_or_keychain"}, nil
	}
	return ProviderStatus{Provider: provider, Source: "environment_or_keychain", Action: "configure", Message: "set " + envName + " or store the provider API key in the OS keychain"}, nil
}

func (m Manager) AllStatus(ctx context.Context) (map[string]ProviderStatus, error) {
	result := make(map[string]ProviderStatus, 3)
	for _, provider := range []string{"qwen", "agy", "claude"} {
		status, err := m.Status(ctx, provider)
		if err != nil {
			return nil, err
		}
		result[provider] = status
	}
	return result, nil
}

func (m Manager) AllStatusWithConfig(ctx context.Context, settings config.Config) (map[string]ProviderStatus, error) {
	result := make(map[string]ProviderStatus, len(settings.Backend))
	for _, provider := range []string{"qwen", "agy", "claude"} {
		status, err := m.StatusWithBackend(ctx, provider, settings.Backend[provider])
		if err != nil {
			return nil, err
		}
		result[provider] = status
	}
	return result, nil
}

func (m Manager) Login(ctx context.Context, provider string) error {
	switch provider {
	case "claude":
		return m.interactive(ctx, "claude", "auth", "login")
	case "qwen":
		return m.interactive(ctx, "codex", "login")
	case "agy":
		return errors.New("agy has no login subcommand; configure VIOLIN_AGY_API_KEY or OS keychain service violin/agy")
	default:
		return fmt.Errorf("unknown provider %q", provider)
	}
}

func (m Manager) LoginWithBackend(ctx context.Context, provider string, backend config.Backend) error {
	if backend.Transport == "api" {
		return errors.New("API transport does not have an interactive login; use environment or OS keychain credentials")
	}
	if len(backend.CLI.LoginCommand) == 0 {
		return fmt.Errorf("%s CLI login is not configured; set backend.%s.cli.login_command", provider, provider)
	}
	return m.interactiveCommand(ctx, backend.CLI.LoginCommand)
}

func (m Manager) SetAPIKey(ctx context.Context, provider, value string) error {
	return m.Store.Set(ctx, "violin/"+apiProvider(provider)+"/api-key", value)
}

func (m Manager) RemoveAPIKey(ctx context.Context, provider string) error {
	return m.Store.Remove(ctx, "violin/"+apiProvider(provider)+"/api-key")
}

func apiProvider(provider string) string { return strings.TrimSuffix(provider, "-api") }

func (m Manager) cliStatus(ctx context.Context, provider, command string, args ...string) (ProviderStatus, error) {
	return m.cliStatusArgs(ctx, provider, append([]string{command}, args...))
}

func (m Manager) cliStatusArgs(ctx context.Context, provider string, command []string) (ProviderStatus, error) {
	if len(command) == 0 {
		return ProviderStatus{Provider: provider, Source: "cli", Action: "configure"}, nil
	}
	data, err := m.Runner(ctx, command[0], command[1:]...)
	if err != nil {
		return ProviderStatus{Provider: provider, Source: "cli", Action: "login", Message: "provider CLI is not authenticated or unavailable"}, nil
	}
	status := ProviderStatus{Provider: provider, Source: "cli"}
	var payload map[string]any
	if json.Unmarshal(data, &payload) == nil {
		for _, key := range []string{"loggedIn", "authenticated", "hasApiKey"} {
			if value, ok := payload[key].(bool); ok {
				status.Authenticated = value
				break
			}
		}
	} else {
		text := strings.ToLower(string(data))
		status.Authenticated = strings.Contains(text, "logged in") || strings.Contains(text, "authenticated")
	}
	if !status.Authenticated {
		status.Action = "login"
	}
	return status, nil
}

func (m Manager) interactive(ctx context.Context, command string, args ...string) error {
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("%s CLI is not installed: %w", command, err)
	}
	return m.interactiveCommand(ctx, append([]string{command}, args...))
}

func (m Manager) interactiveCommand(ctx context.Context, command []string) error {
	if len(command) == 0 {
		return errors.New("empty interactive auth command")
	}
	if _, err := exec.LookPath(command[0]); err != nil {
		return fmt.Errorf("%s CLI is not installed: %w", command[0], err)
	}
	process := exec.CommandContext(ctx, command[0], command[1:]...)
	process.Stdin, process.Stdout, process.Stderr = os.Stdin, os.Stdout, os.Stderr
	return process.Run()
}

func runCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(command); err != nil {
		return nil, err
	}
	statusCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return exec.CommandContext(statusCtx, command, args...).Output()
}
