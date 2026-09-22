package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
	"github.com/film/violin/internal/providers"
)

type Result struct {
	Provider string `json:"provider"`
	Healthy  bool   `json:"healthy"`
	Status   string `json:"status"`
	Evidence any    `json:"evidence,omitempty"`
}

// Check is the single health contract used by the Go CLI and MCP server. A
// configured health command wins, then API transport health, then CLI status.
// Commands are argv arrays and never pass through a shell.
func Check(ctx context.Context, settings config.Config, provider string, store credentials.Store) Result {
	backend := settings.Backend[provider]
	if command := commandParts(backend.HealthCommand); len(command) > 0 {
		return runCommand(ctx, provider, command)
	}
	if backend.Transport == "api" {
		configured, err := providers.FromConfigProvider(ctx, settings, store, provider)
		if err != nil {
			return Result{Provider: provider, Status: "unavailable", Evidence: err.Error()}
		}
		value := configured.Health(ctx)
		return Result{Provider: value.Provider, Healthy: value.Healthy, Status: value.Status, Evidence: value.Evidence}
	}
	if command := backend.CLI.StatusCommand; len(command) > 0 {
		return runCommand(ctx, provider, command)
	}
	return Result{Provider: provider, Status: "unconfigured", Evidence: "configure backend health_command, API transport, or CLI status_command"}
}

func commandParts(value any) []string {
	switch typed := value.(type) {
	case string:
		return strings.Fields(typed)
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, fmt.Sprint(item))
		}
		return result
	default:
		return nil
	}
}

func runCommand(parent context.Context, provider string, command []string) Result {
	if len(command) == 0 {
		return Result{Provider: provider, Status: "unconfigured"}
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, command[0], command[1:]...).CombinedOutput()
	if err != nil {
		status := "unavailable"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = "timeout"
		}
		return Result{Provider: provider, Status: status, Evidence: map[string]any{"command": command[0], "error": err.Error()}}
	}
	var evidence any
	if json.Unmarshal(output, &evidence) != nil {
		evidence = strings.TrimSpace(string(output))
	}
	return Result{Provider: provider, Healthy: true, Status: "healthy", Evidence: evidence}
}
