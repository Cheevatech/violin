package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeProviderParsers(t *testing.T) {
	qwen, err := parseOpenAIResponse("qwen")([]byte(`{"choices":[{"message":{"content":"qwen ok"}}]}`))
	if err != nil || qwen.Text != "qwen ok" {
		t.Fatalf("qwen=%+v err=%v", qwen, err)
	}
	agy, err := parseGeminiResponse([]byte(`{"candidates":[{"content":{"parts":[{"text":"agy ok"}]}}]}`))
	if err != nil || agy.Text != "agy ok" {
		t.Fatalf("agy=%+v err=%v", agy, err)
	}
	claude, err := parseClaudeResponse([]byte(`{"content":[{"text":"claude ok"}]}`))
	if err != nil || claude.Text != "claude ok" {
		t.Fatalf("claude=%+v err=%v", claude, err)
	}
	qwenProvider := NewQwen("https://qwen.test", "secret", "model")
	if qwenProvider.Name() != "qwen" || qwenProvider.Header != "Authorization" || qwenProvider.APIKey != "Bearer secret" {
		t.Fatalf("unexpected qwen provider: %+v", qwenProvider)
	}
	responses, err := parseResponsesResponse([]byte(`{"output":[{"content":[{"text":"responses ok"}]}]}`))
	if err != nil || responses.Text != "responses ok" {
		t.Fatalf("responses=%+v err=%v", responses, err)
	}
	agyProvider := NewAGY("https://agy.test", "secret", "model")
	if agyProvider.Header != "x-goog-api-key" || agyProvider.BaseURL != "https://agy.test/v1beta/models/model:generateContent" {
		t.Fatalf("unexpected agy provider: %+v", agyProvider)
	}
	qwenResponses := NewQwenWithWireAPI("https://qwen.test", "secret", "model", "responses")
	if qwenResponses.BaseURL != "https://qwen.test/v1/responses" {
		t.Fatalf("unexpected qwen responses provider: %+v", qwenResponses)
	}
}

func TestNativeProvidersExecuteAgainstOfflineHTTPFixtures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("invalid request body: %v", err)
		}
		if request.Header.Get("Authorization") == "Bearer qwen-key" {
			if request.URL.Path != "/v1/responses" || payload["model"] != "model" || payload["input"] != "test" {
				t.Errorf("qwen request path=%s payload=%v", request.URL.Path, payload)
			}
			_, _ = writer.Write([]byte(`{"output":[{"content":[{"text":"qwen e2e"}]}]}`))
			return
		}
		if request.Header.Get("x-goog-api-key") == "agy-key" {
			if request.URL.Path != "/v1beta/models/gemini:generateContent" || request.URL.RawQuery != "" || payload["contents"] == nil {
				t.Errorf("agy endpoint=%s?%s payload=%v", request.URL.Path, request.URL.RawQuery, payload)
			}
			_, _ = writer.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"agy e2e"}]}}]}`))
			return
		}
		if request.Header.Get("x-api-key") == "claude-key" {
			if request.URL.Path != "/v1/messages" || payload["model"] != "model" || payload["messages"] == nil {
				t.Errorf("claude path=%s payload=%v", request.URL.Path, payload)
			}
			_, _ = writer.Write([]byte(`{"content":[{"text":"claude e2e"}]}`))
			return
		}
		http.Error(writer, "missing provider key", http.StatusUnauthorized)
	}))
	defer server.Close()
	for _, fixture := range []struct {
		name, want string
		provider   HTTPProvider
	}{
		{"qwen", "qwen e2e", NewQwenWithWireAPI(server.URL, "qwen-key", "model", "responses")},
		{"agy", "agy e2e", NewAGY(server.URL, "agy-key", "gemini")},
		{"claude", "claude e2e", NewClaude(server.URL, "claude-key", "model")},
	} {
		result, err := fixture.provider.Execute(context.Background(), Request{Task: "test", Timeout: time.Second})
		if err != nil || result.Text != fixture.want {
			t.Errorf("%s result=%+v err=%v", fixture.name, result, err)
		}
		if strings.TrimSpace(fixture.provider.APIKey) == "" {
			t.Errorf("%s key was not configured", fixture.name)
		}
	}
}
