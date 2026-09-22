package providers

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func NewQwen(baseURL, apiKey, model string) HTTPProvider {
	return HTTPProvider{
		NameValue: "qwen", BaseURL: strings.TrimRight(baseURL, "/") + "/v1/chat/completions", APIKey: "Bearer " + apiKey, Header: "Authorization",
		BuildBody: func(request Request) (any, error) {
			return map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": request.Task}}, "stream": false}, nil
		},
		ParseBody: parseOpenAIResponse("qwen"),
	}
}

func NewAGY(baseURL, apiKey, model string) HTTPProvider {
	endpoint := strings.TrimRight(baseURL, "/") + "/v1beta/models/" + url.PathEscape(model) + ":generateContent?key=" + url.QueryEscape(apiKey)
	return HTTPProvider{
		NameValue: "agy", BaseURL: endpoint, Header: "",
		BuildBody: func(request Request) (any, error) {
			return map[string]any{"contents": []map[string]any{{"role": "user", "parts": []map[string]string{{"text": request.Task}}}}}, nil
		},
		ParseBody: parseGeminiResponse,
	}
}

func NewClaude(baseURL, apiKey, model string) HTTPProvider {
	return HTTPProvider{
		NameValue: "claude", BaseURL: strings.TrimRight(baseURL, "/") + "/v1/messages", APIKey: apiKey, Header: "x-api-key",
		BuildBody: func(request Request) (any, error) {
			return map[string]any{"model": model, "max_tokens": 4096, "messages": []map[string]string{{"role": "user", "content": request.Task}}}, nil
		},
		ParseBody: parseClaudeResponse,
	}
}

func parseOpenAIResponse(name string) func([]byte) (Result, error) {
	return func(data []byte) (Result, error) {
		var payload struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage any `json:"usage"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return Result{}, err
		}
		if len(payload.Choices) == 0 {
			return Result{}, fmt.Errorf("%s response has no choices", name)
		}
		return Result{Provider: name, Status: "completed", Text: payload.Choices[0].Message.Content, Usage: payload.Usage}, nil
	}
}

func parseGeminiResponse(data []byte) (Result, error) {
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Result{}, err
	}
	if len(payload.Candidates) == 0 || len(payload.Candidates[0].Content.Parts) == 0 {
		return Result{}, fmt.Errorf("agy response has no candidate text")
	}
	return Result{Provider: "agy", Status: "completed", Text: payload.Candidates[0].Content.Parts[0].Text}, nil
}

func parseClaudeResponse(data []byte) (Result, error) {
	var payload struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage any `json:"usage"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Result{}, err
	}
	if len(payload.Content) == 0 {
		return Result{}, fmt.Errorf("claude response has no content")
	}
	return Result{Provider: "claude", Status: "completed", Text: payload.Content[0].Text, Usage: payload.Usage}, nil
}
