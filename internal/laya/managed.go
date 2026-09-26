package laya

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/film/violin/internal/models"
)

// ManagedEngine provides Violin's Go classifier fallback when upstream ONNX
// inference is unavailable. It never starts a child process.
type ManagedEngine struct {
	Manager   *models.Manager
	ModelPath string
	Fallback  FallbackEngine
}

func (e ManagedEngine) Evaluate(request Request) (Result, error) {
	modelPath := e.ModelPath
	if e.Manager != nil {
		status := e.Manager.Status()
		if !status.Verified {
			return e.fallback(request, ErrUnavailable)
		}
		if modelPath == "" {
			modelPath, _ = e.Manager.ActivePath()
		}
	}
	if modelPath == "" {
		return e.fallback(request, ErrUnavailable)
	}
	model, err := LoadModel(filepath.Join(modelPath, "model.json"))
	if err != nil {
		return e.fallback(request, err)
	}
	return model.Evaluate(request)
}

func (e ManagedEngine) fallback(request Request, cause error) (Result, error) {
	if cause == nil {
		cause = errors.New("Go classifier fallback is unavailable")
	}
	fallback := e.Fallback
	if fallback.ModelVersion == "" {
		fallback.ModelVersion = os.Getenv("VIOLIN_LAYA_MODEL_VERSION")
	}
	result, err := fallback.Evaluate(request)
	result.Fallback = true
	result.Error = cause.Error()
	if err != nil {
		return result, err
	}
	return result, nil
}
