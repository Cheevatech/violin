package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/film/violin/internal/credentials"
)

func TestClaudeStatusReadsCLIStateWithoutReturningOutput(t *testing.T) {
	manager := Manager{Store: credentials.Store{}, Runner: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"loggedIn":true,"email":"user@example.com"}`), nil
	}}
	status, err := manager.Status(context.Background(), "claude")
	if err != nil || !status.Authenticated || status.Message != "" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestAgyStatusUsesGlobalCredentialStore(t *testing.T) {
	manager := Manager{Store: credentials.Store{Env: map[string]string{"VIOLIN_AGY_API_KEY": "secret"}}}
	status, err := manager.Status(context.Background(), "agy")
	if err != nil || !status.Authenticated || status.Source != "environment_or_keychain" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestAgyLoginDoesNotPretendCLIAuthExists(t *testing.T) {
	err := (Manager{}).Login(context.Background(), "agy")
	if err == nil || !strings.Contains(err.Error(), "no login subcommand") {
		t.Fatalf("unexpected error: %v", err)
	}
}
