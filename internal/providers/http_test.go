package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPProviderExecute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "secret" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.Header.Get("Authorization"))
		}
		_, _ = writer.Write([]byte(`{"text":"ok"}`))
	}))
	defer server.Close()
	provider := HTTPProvider{NameValue: "fake", BaseURL: server.URL, APIKey: "secret", Header: "Authorization", ParseBody: func(data []byte) (Result, error) {
		return Result{Provider: "fake", Status: "completed", Text: string(data)}, nil
	}}
	result, err := provider.Execute(context.Background(), Request{Task: "test", Timeout: time.Second})
	if err != nil || result.Status != "completed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestHTTPProviderHealthDoesNotExposeKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	result := (HTTPProvider{NameValue: "fake", BaseURL: server.URL, APIKey: "secret", Header: "Authorization"}).Health(context.Background())
	if result.Evidence == "secret" || result.Healthy {
		t.Fatalf("unsafe health result: %+v", result)
	}
}
