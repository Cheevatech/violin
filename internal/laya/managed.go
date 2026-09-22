package laya

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/film/violin/internal/models"
)

// ManagedEngine keeps model ownership inside violin while allowing the
// inference implementation to evolve independently of the control plane.
// When no verified runtime is configured it is deliberately fail-safe and
// returns typed fallback answers rather than silently pretending Laya ran.
type ManagedEngine struct {
	Manager   *models.Manager
	Runner    []string
	ModelPath string
	Timeout   time.Duration
	Fallback  FallbackEngine
}

func (e ManagedEngine) Evaluate(request Request) (Result, error) {
	status := models.Status{}
	if e.Manager != nil {
		status = e.Manager.Status()
		if !status.Verified {
			return e.fallback(request, ErrUnavailable)
		}
		if e.ModelPath == "" {
			e.ModelPath, _ = e.Manager.ActivePath()
		}
	}
	if len(e.Runner) == 0 && e.ModelPath != "" {
		model, err := LoadModel(filepath.Join(e.ModelPath, "model.json"))
		if err != nil {
			return e.fallback(request, err)
		}
		return model.Evaluate(request)
	}
	if len(e.Runner) == 0 {
		return e.fallback(request, ErrUnavailable)
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	commandContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(commandContext, e.Runner[0], e.Runner[1:]...)
	command.Env = append(os.Environ(), "VIOLIN_LAYA_MODEL_DIR="+e.ModelPath)
	input, err := json.Marshal(request)
	if err != nil {
		return e.fallback(request, err)
	}
	command.Stdin = strings.NewReader(string(input) + "\n")
	output, err := command.Output()
	if err != nil {
		if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
			return e.fallback(request, errors.New("Laya runner timed out"))
		}
		return e.fallback(request, err)
	}
	var result Result
	if err = json.Unmarshal(output, &result); err != nil {
		return e.fallback(request, err)
	}
	if result.ModelVersion == "" && status.Manifest != nil {
		result.ModelVersion = status.Manifest.Version
	}
	decision, err := result.NormalizedDecision(request)
	if err != nil {
		return e.fallback(request, err)
	}
	result.Decision = &decision
	return result, nil
}

func RunnerFromEnv() []string {
	value := strings.TrimSpace(os.Getenv("VIOLIN_LAYA_RUNNER"))
	if value == "" {
		return nil
	}
	var runner []string
	if json.Unmarshal([]byte(value), &runner) != nil || len(runner) == 0 || strings.TrimSpace(runner[0]) == "" {
		return nil
	}
	return runner
}

func (e ManagedEngine) fallback(request Request, err error) (Result, error) {
	fallback := e.Fallback
	if fallback.ModelVersion == "" {
		fallback.ModelVersion = os.Getenv("VIOLIN_LAYA_MODEL_VERSION")
	}
	result, fallbackErr := fallback.Evaluate(request)
	result.Error = err.Error()
	if fallbackErr != nil {
		return result, fallbackErr
	}
	return result, nil
}
