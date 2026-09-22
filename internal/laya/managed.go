package laya

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"

	"github.com/film/violin/internal/models"
)

// ManagedEngine keeps model ownership inside violin while allowing the
// inference implementation to evolve independently of the control plane.
// When no verified runtime is configured it is deliberately fail-safe and
// returns typed fallback answers rather than silently pretending Laya ran.
type ManagedEngine struct {
	Manager  *models.Manager
	Runner   []string
	Fallback FallbackEngine
}

func (e ManagedEngine) Evaluate(request Request) (Result, error) {
	status := models.Status{}
	if e.Manager != nil {
		status = e.Manager.Status()
		if !status.Verified {
			return e.fallback(request, ErrUnavailable)
		}
	}
	if len(e.Runner) == 0 {
		return e.fallback(request, ErrUnavailable)
	}
	command := exec.Command(e.Runner[0], e.Runner[1:]...)
	input, err := json.Marshal(request)
	if err != nil {
		return e.fallback(request, err)
	}
	command.Stdin = strings.NewReader(string(input) + "\n")
	output, err := command.Output()
	if err != nil {
		return e.fallback(request, err)
	}
	var result Result
	if err = json.Unmarshal(output, &result); err != nil {
		return e.fallback(request, err)
	}
	if result.ModelVersion == "" && status.Manifest != nil {
		result.ModelVersion = status.Manifest.Version
	}
	return result, nil
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
