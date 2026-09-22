package providers

import (
	"context"
	"os"
	"strings"

	"github.com/film/violin/internal/credentials"
)

func FromEnvironment(ctx context.Context, store credentials.Store) (map[string]Provider, error) {
	qwenKey, err := store.Lookup(ctx, "VIOLIN_QWEN_API_KEY", "violin/qwen")
	if err != nil {
		return nil, err
	}
	agyKey, err := store.Lookup(ctx, "VIOLIN_AGY_API_KEY", "violin/agy")
	if err != nil {
		return nil, err
	}
	claudeKey, err := store.Lookup(ctx, "VIOLIN_CLAUDE_API_KEY", "violin/claude")
	if err != nil {
		return nil, err
	}
	return map[string]Provider{
		"qwen":   NewQwen(requiredEnv("VIOLIN_QWEN_BASE_URL"), qwenKey, envOr("VIOLIN_QWEN_MODEL", "qwen")),
		"agy":    NewAGY(envOr("VIOLIN_AGY_BASE_URL", "https://generativelanguage.googleapis.com"), agyKey, envOr("VIOLIN_AGY_MODEL", "gemini-2.5-flash")),
		"claude": NewClaude(envOr("VIOLIN_CLAUDE_BASE_URL", "https://api.anthropic.com"), claudeKey, envOr("VIOLIN_CLAUDE_MODEL", "claude-sonnet-4-5")),
	}, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string { return strings.TrimSpace(os.Getenv(name)) }
