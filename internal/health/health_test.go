package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
