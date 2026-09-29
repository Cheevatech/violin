package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
)

func FromEnvironment(ctx context.Context, store credentials.Store) (map[string]Provider, error) {
	return FromConfig(ctx, config.Defaults(), store)
}

func FromConfig(ctx context.Context, settings config.Config, store credentials.Store) (map[string]Provider, error) {
	result := make(map[string]Provider, 3)
	for _, name := range []string{"qwen", "agy", "claude"} {
		provider, err := FromConfigProvider(ctx, settings, store, name)
		if err != nil {
			return nil, err
		}
		result[name] = provider
	}
	return result, nil
}

func FromConfigProvider(ctx context.Context, settings config.Config, store credentials.Store, name string) (Provider, error) {
	qwenBackend := settings.Backend["qwen"]
	agyBackend := settings.Backend["agy"]
	claudeBackend := settings.Backend["claude"]
	qwen, agy, claude := qwenBackend.API, agyBackend.API, claudeBackend.API
	switch name {
	case "qwen":
		key, err := lookupAPIKey(ctx, store, name, qwenBackend.EffectiveAPIKeyEnv())
		if err != nil {
			return nil, err
		}
		return NewQwenWithWireAPI(qwen.BaseURL, key, envOrValue(qwenBackend.EffectiveModel(), "qwen"), qwen.WireAPI), nil
	case "agy":
		key, err := lookupAPIKey(ctx, store, name, agyBackend.EffectiveAPIKeyEnv())
		if err != nil {
			return nil, err
		}
		return NewAGY(envOrValue(agy.BaseURL, "https://generativelanguage.googleapis.com"), key, envOrValue(agyBackend.EffectiveModel(), "gemini-2.5-flash")), nil
	case "claude":
		key, err := lookupAPIKey(ctx, store, name, claudeBackend.EffectiveAPIKeyEnv())
		if err != nil {
			return nil, err
		}
		return NewClaude(envOrValue(claude.BaseURL, "https://api.anthropic.com"), key, envOrValue(claudeBackend.EffectiveModel(), "claude-sonnet-4-5")), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", name)
	}
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
