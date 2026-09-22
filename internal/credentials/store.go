package credentials

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var ErrNotFound = errors.New("credential not found")

type Store struct {
	Env      map[string]string
	OS       string
	Keychain func(context.Context, string) (string, error)
}

func Default() Store {
	return Store{Env: map[string]string{}, OS: runtime.GOOS, Keychain: keychainLookup}
}

func (s Store) Lookup(ctx context.Context, envName, service string) (string, error) {
	if value := strings.TrimSpace(s.environment(envName)); value != "" {
		return value, nil
	}
	if s.Keychain != nil {
		if value, err := s.Keychain(ctx, service); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, service)
}

func (s Store) environment(name string) string {
	if s.Env != nil {
		if value, ok := s.Env[name]; ok {
			return value
		}
	}
	return os.Getenv(name)
}

func keychainLookup(ctx context.Context, service string) (string, error) {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "security", []string{"find-generic-password", "-s", service, "-w"}
	case "linux":
		command, args = "secret-tool", []string{"lookup", "service", service}
	default:
		return "", errors.New("OS keychain is unsupported on this platform")
	}
	if _, err := exec.LookPath(command); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
