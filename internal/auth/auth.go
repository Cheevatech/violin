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

func (m Manager) cliStatus(ctx context.Context, provider, command string, args ...string) (ProviderStatus, error) {
	data, err := m.Runner(ctx, command, args...)
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
	process := exec.CommandContext(ctx, command, args...)
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
