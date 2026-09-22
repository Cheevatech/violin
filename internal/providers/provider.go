package providers

import (
	"context"
	"time"
)

type HealthResult struct {
	Provider string `json:"provider"`
	Healthy  bool   `json:"healthy"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

type Request struct {
	Task      string
	Workspace string
	Mode      string
	Timeout   time.Duration
}

type Result struct {
	Provider string `json:"provider"`
	Status   string `json:"status"`
	Text     string `json:"text,omitempty"`
	Usage    any    `json:"usage,omitempty"`
}

type Provider interface {
	Name() string
	Health(context.Context) HealthResult
	Execute(context.Context, Request) (Result, error)
	Interrupt(context.Context, string) error
}
