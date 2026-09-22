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
	Env            map[string]string
	OS             string
	Keychain       func(context.Context, string) (string, error)
	SetKeychain    func(context.Context, string, string) error
	DeleteKeychain func(context.Context, string) error
}

func Default() Store {
	return Store{Env: map[string]string{}, OS: runtime.GOOS, Keychain: keychainLookup, SetKeychain: keychainSet, DeleteKeychain: keychainDelete}
}

func (s Store) Set(ctx context.Context, service, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("credential value is empty")
	}
	if s.SetKeychain == nil {
		return errors.New("OS keychain is unavailable")
	}
	return s.SetKeychain(ctx, service, value)
}

func (s Store) Remove(ctx context.Context, service string) error {
	if s.DeleteKeychain == nil {
		return errors.New("OS keychain is unavailable")
	}
	return s.DeleteKeychain(ctx, service)
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

func keychainSet(ctx context.Context, service, value string) error {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.CommandContext(ctx, "security", "add-generic-password", "-a", os.Getenv("USER"), "-s", service, "-w", value, "-U")
		return cmd.Run()
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, "secret-tool", "store", "--label", service, "service", service)
		cmd.Stdin = strings.NewReader(value)
		return cmd.Run()
	default:
		return errors.New("OS keychain is unsupported on this platform")
	}
}

func keychainDelete(ctx context.Context, service string) error {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.CommandContext(ctx, "security", "delete-generic-password", "-a", os.Getenv("USER"), "-s", service)
		return cmd.Run()
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return err
		}
		return exec.CommandContext(ctx, "secret-tool", "clear", "service", service).Run()
	default:
		return errors.New("OS keychain is unsupported on this platform")
	}
}
