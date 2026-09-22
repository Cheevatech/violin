package providers

import "testing"

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
