package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
)

func TestCheckUsesConfiguredAPIProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("VIOLIN_HEALTH_KEY", "secret")
	settings := config.Defaults()
	qwen := settings.Backend["qwen"]
	qwen.Transport = "api"
	qwen.API.BaseURL = server.URL
	qwen.API.APIKeyEnv = "VIOLIN_HEALTH_KEY"
	settings.Backend["qwen"] = qwen
	result := Check(context.Background(), settings, "qwen", credentials.Default())
	if !result.Healthy || result.Provider != "qwen" {
		t.Fatalf("result=%+v", result)
	}
}

func TestHealthCommandFailurePreservesOnlySanitizedJSONDiagnostics(t *testing.T) {
	settings := config.Defaults()
	qwen := settings.Backend["qwen"]
	qwen.HealthCommand = []string{"/bin/sh", "-c", "printf '{\"status\":\"qwen_unhealthy\",\"error\":\"smoke_timeout\",\"secret\":\"do-not-copy\"}'; exit 79"}
	settings.Backend["qwen"] = qwen
	result := Check(context.Background(), settings, "qwen", credentials.Default())
	data, err := json.Marshal(result.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if result.Healthy || result.Status != "qwen_unhealthy" || !strings.Contains(string(data), "smoke_timeout") || strings.Contains(string(data), "do-not-copy") {
		t.Fatalf("unexpected sanitized failure: %+v", result)
	}
}

func TestHealthCommandHonorsParentDeadline(t *testing.T) {
	settings := config.Defaults()
	qwen := settings.Backend["qwen"]
	qwen.HealthCommand = []string{"/bin/sh", "-c", "sleep 2"}
	settings.Backend["qwen"] = qwen
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := Check(ctx, settings, "qwen", credentials.Default())
	if result.Status != "timeout" || time.Since(started) > time.Second {
		t.Fatalf("deadline not honored: result=%+v elapsed=%s", result, time.Since(started))
	}
}

func TestCheckUsesConfiguredHealthCommand(t *testing.T) {
	settings := config.Defaults()
	claude := settings.Backend["claude"]
	claude.HealthCommand = []string{"/bin/sh", "-c", "printf '{\"ready\":true}'"}
	settings.Backend["claude"] = claude
	result := Check(context.Background(), settings, "claude", credentials.Default())
	if !result.Healthy || result.Status != "healthy" {
		t.Fatalf("result=%+v", result)
	}
}

func TestQwenHealthCommandReceivesKeychainCredentialFromStore(t *testing.T) {
	settings := config.Defaults()
	qwen := settings.Backend["qwen"]
	qwen.HealthCommand = []string{"/bin/sh", "-c", "test \"$LLMUX_API_KEY\" = health-test-key && printf '{\"status\":\"ready\"}'"}
	settings.Backend["qwen"] = qwen
	store := credentials.Store{Env: map[string]string{"LLMUX_API_KEY": "health-test-key"}}
	result := Check(context.Background(), settings, "qwen", store)
	if !result.Healthy {
		t.Fatalf("health command did not receive injected credential: %+v", result)
	}
}
