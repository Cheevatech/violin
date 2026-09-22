package providers

import (
	"context"
	"strings"

	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
)

func FromEnvironment(ctx context.Context, store credentials.Store) (map[string]Provider, error) {
	return FromConfig(ctx, config.Defaults(), store)
}

func FromConfig(ctx context.Context, settings config.Config, store credentials.Store) (map[string]Provider, error) {
	qwen := settings.Backend["qwen"].API
	agy := settings.Backend["agy"].API
	claude := settings.Backend["claude"].API
	qwenKey, err := lookupAPIKey(ctx, store, "qwen", qwen.APIKeyEnv)
	if err != nil {
		return nil, err
	}
	agyKey, err := lookupAPIKey(ctx, store, "agy", agy.APIKeyEnv)
	if err != nil {
		return nil, err
	}
	claudeKey, err := lookupAPIKey(ctx, store, "claude", claude.APIKeyEnv)
	if err != nil {
		return nil, err
	}
	return map[string]Provider{
		"qwen":   NewQwen(qwen.BaseURL, qwenKey, envOrValue(qwen.Model, "qwen")),
		"agy":    NewAGY(envOrValue(agy.BaseURL, "https://generativelanguage.googleapis.com"), agyKey, envOrValue(agy.Model, "gemini-2.5-flash")),
		"claude": NewClaude(envOrValue(claude.BaseURL, "https://api.anthropic.com"), claudeKey, envOrValue(claude.Model, "claude-sonnet-4-5")),
	}, nil
}

func lookupAPIKey(ctx context.Context, store credentials.Store, provider, configuredEnv string) (string, error) {
	envName := configuredEnv
	if envName == "" {
		envName = "VIOLIN_" + strings.ToUpper(provider) + "_API_KEY"
	}
	return store.Lookup(ctx, envName, "violin/"+provider+"/api-key")
}

func envOrValue(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
