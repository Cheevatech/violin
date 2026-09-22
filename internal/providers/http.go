package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/film/violin/internal/credentials"
)

type HTTPProvider struct {
	NameValue string
	BaseURL   string
	APIKey    string
	Header    string
	Client    *http.Client
	BuildBody func(Request) (any, error)
	ParseBody func([]byte) (Result, error)
}

func (p HTTPProvider) Name() string { return p.NameValue }

func (p HTTPProvider) Health(ctx context.Context) HealthResult {
	if strings.TrimSpace(p.BaseURL) == "" {
		return HealthResult{Provider: p.Name(), Status: "unconfigured", Evidence: "base URL is not configured"}
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/"), nil)
	if err != nil {
		return HealthResult{Provider: p.Name(), Status: "invalid", Evidence: err.Error()}
	}
	if p.APIKey != "" {
		request.Header.Set(p.Header, p.APIKey)
	}
	response, err := client.Do(request)
	if err != nil {
		return HealthResult{Provider: p.Name(), Status: "unavailable", Evidence: "provider request failed"}
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 500 {
		return HealthResult{Provider: p.Name(), Healthy: response.StatusCode < 400, Status: response.Status, Evidence: "endpoint responded"}
	}
	return HealthResult{Provider: p.Name(), Status: response.Status, Evidence: "endpoint returned server error"}
}

func (p HTTPProvider) Execute(ctx context.Context, request Request) (Result, error) {
	if strings.TrimSpace(p.BaseURL) == "" {
		return Result{}, errors.New("provider base URL is not configured")
	}
	body := any(map[string]any{"task": request.Task, "workspace": request.Workspace, "mode": request.Mode})
	var err error
	if p.BuildBody != nil {
		body, err = p.BuildBody(request)
		if err != nil {
			return Result{}, err
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := withTimeout(ctx, request.Timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/"), bytes.NewReader(data))
	if err != nil {
		return Result{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpRequest.Header.Set(p.Header, p.APIKey)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	responseData, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return Result{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("%s returned HTTP %d", p.Name(), response.StatusCode)
	}
	if p.ParseBody != nil {
		return p.ParseBody(responseData)
	}
	return Result{Provider: p.Name(), Status: "completed", Text: string(responseData)}, nil
}

func (p HTTPProvider) Interrupt(context.Context, string) error { return nil }

func NewFromCredential(ctx context.Context, name, baseURL, envName, header string, store credentials.Store) (HTTPProvider, error) {
	key, err := store.Lookup(ctx, envName, "violin/"+name)
	if err != nil {
		return HTTPProvider{}, err
	}
	return HTTPProvider{NameValue: name, BaseURL: baseURL, APIKey: key, Header: header}, nil
}

func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
