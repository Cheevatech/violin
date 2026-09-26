package laya

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalLLMEvaluateReturnsStructuredRoutingDecision(t *testing.T) {
	decision := Decision{
		BackendCandidates: []string{"qwen"}, TaskMode: "inspect", Risk: RiskLow,
		TimeoutHintSeconds: 900, IdleTimeoutEnabled: false,
		Retry: RetryHint{MaxAttempts: 1}, ExecutionTarget: ExecutionExternal,
		CostTier: TierMedium, LatencyTier: TierMedium, Confidence: 0.8, Margin: 0.2,
		HeadConfidence: map[string]float64{"backend": 0.9, "task_mode": 0.9, "risk": 0.9, "timeout_policy": 0.9, "retry_policy": 0.9},
		HeadMargin:     map[string]float64{"backend": 0.4, "task_mode": 0.4, "risk": 0.4, "timeout_policy": 0.4, "retry_policy": 0.4},
		ReasonCodes:    []string{"inspect_task"},
	}
	encodedDecision, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing local runtime authorization")
		}
		var request localCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.ResponseFormat["type"] != "json_schema" || request.MaxTokens != 384 {
			t.Errorf("unexpected generation constraints: %+v", request)
		}
		if !strings.Contains(request.Messages[1].Content, "review this patch") || !strings.Contains(request.Messages[1].Content, "/no_think") {
			t.Errorf("task or no-think instruction missing: %+v", request.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + string(mustJSONQuote(string(encodedDecision))) + `}}],"usage":{"prompt_tokens":10,"completion_tokens":30}}`))
	}))
	defer server.Close()

	engine := &LocalLLM{client: server.Client(), endpoint: server.URL, apiKey: "test-key", version: LocalModelVersion}
	result, err := engine.Evaluate(Request{Language: "en", State: map[string]any{"task": "review this patch", "mode": "inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision == nil || result.Decision.BackendCandidates[0] != "qwen" || result.ModelVersion != LocalModelVersion || result.Fallback || result.Decision.HeadConfidence["risk"] != 0.9 {
		t.Fatalf("unexpected local LLM result: %+v", result)
	}
}

func mustJSONQuote(value string) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
