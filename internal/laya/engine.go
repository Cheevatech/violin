package laya

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

const ProtocolLanguage = "en"

type Kind string

const (
	Choice Kind = "choice"
	Score  Kind = "score"
	Noul   Kind = "noul"
)

type Question struct {
	ID       string   `json:"id"`
	Kind     Kind     `json:"kind"`
	Prompt   string   `json:"prompt"`
	Options  []string `json:"options,omitempty"`
	Levels   []int    `json:"levels,omitempty"`
	Fallback string   `json:"fallback,omitempty"`
}
type Request struct {
	Language     string     `json:"language,omitempty"`
	State        any        `json:"state"`
	Questions    []Question `json:"questions"`
	ModelVersion string     `json:"model_version,omitempty"`
}
type Answer struct {
	ID            string             `json:"id"`
	Kind          Kind               `json:"kind"`
	Value         any                `json:"value"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence"`
	Fallback      bool               `json:"fallback"`
}

type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

type ExecutionTarget string

const (
	ExecutionLocal    ExecutionTarget = "local"
	ExecutionExternal ExecutionTarget = "external"
)

type RetryHint struct {
	MaxAttempts int `json:"max_attempts"`
	BackoffSecs int `json:"backoff_seconds"`
}

type Decision struct {
	BackendCandidates  []string        `json:"backend_candidates"`
	TaskMode           string          `json:"task_mode"`
	Risk               Risk            `json:"risk"`
	TimeoutHintSeconds int             `json:"timeout_hint_seconds"`
	IdleTimeoutEnabled bool            `json:"idle_timeout_enabled"`
	Retry              RetryHint       `json:"retry_hint"`
	ExecutionTarget    ExecutionTarget `json:"execution_target"`
	Confidence         float64         `json:"confidence"`
	Margin             float64         `json:"margin"`
	ReasonCodes        []string        `json:"reason_codes"`
	ModelVersion       string          `json:"model_version"`
	Fallback           bool            `json:"fallback"`
}

func (d Decision) Validate() error {
	if len(d.BackendCandidates) == 0 {
		return errors.New("Laya decision requires backend candidates")
	}
	seen := map[string]bool{}
	for _, backend := range d.BackendCandidates {
		if backend != "agy" && backend != "qwen" && backend != "claude" {
			return fmt.Errorf("unsupported Laya backend %q", backend)
		}
		if seen[backend] {
			return fmt.Errorf("duplicate Laya backend %q", backend)
		}
		seen[backend] = true
	}
	if d.TaskMode != "inspect" && d.TaskMode != "implement" {
		return fmt.Errorf("unsupported Laya task mode %q", d.TaskMode)
	}
	if d.Risk != RiskLow && d.Risk != RiskMedium && d.Risk != RiskHigh {
		return fmt.Errorf("unsupported Laya risk %q", d.Risk)
	}
	if d.TimeoutHintSeconds < 1 {
		return errors.New("Laya timeout hint must be positive")
	}
	if d.Retry.MaxAttempts < 1 || d.Retry.MaxAttempts > 3 || d.Retry.BackoffSecs < 0 {
		return errors.New("Laya retry hint is outside safe bounds")
	}
	if d.ExecutionTarget != ExecutionLocal && d.ExecutionTarget != ExecutionExternal {
		return fmt.Errorf("unsupported Laya execution target %q", d.ExecutionTarget)
	}
	if math.IsNaN(d.Confidence) || math.IsInf(d.Confidence, 0) || d.Confidence < 0 || d.Confidence > 1 {
		return errors.New("Laya confidence must be between 0 and 1")
	}
	if math.IsNaN(d.Margin) || math.IsInf(d.Margin, 0) || d.Margin < 0 || d.Margin > 1 {
		return errors.New("Laya margin must be between 0 and 1")
	}
	return nil
}

type Result struct {
	Answers      []Answer  `json:"answers"`
	Decision     *Decision `json:"decision,omitempty"`
	ModelVersion string    `json:"model_version"`
	LatencyMS    float64   `json:"latency_ms"`
	Fallback     bool      `json:"fallback"`
	Error        string    `json:"error,omitempty"`
}

func (r Result) NormalizedDecision(request Request) (Decision, error) {
	if r.Decision != nil {
		decision := *r.Decision
		if decision.ModelVersion == "" {
			decision.ModelVersion = r.ModelVersion
		}
		if err := decision.Validate(); err != nil {
			return Decision{}, err
		}
		return decision, nil
	}
	for _, answer := range r.Answers {
		if answer.ID != "backend" {
			continue
		}
		backend, ok := answer.Value.(string)
		if !ok || backend == "" {
			break
		}
		decision := Decision{
			BackendCandidates:  []string{backend},
			TaskMode:           "inspect",
			Risk:               RiskLow,
			TimeoutHintSeconds: 900,
			IdleTimeoutEnabled: true,
			Retry:              RetryHint{MaxAttempts: 1},
			ExecutionTarget:    ExecutionExternal,
			Confidence:         answer.Confidence,
			ModelVersion:       r.ModelVersion,
			Fallback:           r.Fallback || answer.Fallback,
		}
		if request.State != nil {
			if state, ok := request.State.(map[string]any); ok {
				if mode, ok := state["mode"].(string); ok && (mode == "inspect" || mode == "implement") {
					decision.TaskMode = mode
				}
			}
		}
		if err := decision.Validate(); err != nil {
			return Decision{}, err
		}
		return decision, nil
	}
	return Decision{}, errors.New("Laya result has no decision")
}

type Engine interface{ Evaluate(Request) (Result, error) }

var ErrUnavailable = errors.New("laya model unavailable")

type FallbackEngine struct{ ModelVersion string }

func (e FallbackEngine) Evaluate(request Request) (Result, error) {
	if strings.TrimSpace(request.Language) == "" {
		request.Language = ProtocolLanguage
	}
	if strings.ToLower(strings.TrimSpace(request.Language)) != ProtocolLanguage {
		return Result{ModelVersion: e.ModelVersion, Fallback: true}, fmt.Errorf("unsupported Laya protocol language %q: Violin requires English (en)", request.Language)
	}
	result := Result{ModelVersion: e.ModelVersion, Fallback: true}
	for _, q := range request.Questions {
		answer := Answer{ID: q.ID, Kind: q.Kind, Confidence: 0, Fallback: true}
		switch q.Kind {
		case Choice:
			if len(q.Options) == 0 {
				return result, errors.New("choice question has no options")
			}
			selected := q.Fallback
			if selected == "" {
				selected = q.Options[0]
			}
			answer.Value = selected
			answer.Probabilities = map[string]float64{selected: 1}
		case Score:
			if len(q.Levels) == 0 {
				return result, errors.New("score question has no levels")
			}
			answer.Value = q.Levels[0]
		case Noul:
			answer.Value = false
		default:
			return result, errors.New("unknown question kind")
		}
		result.Answers = append(result.Answers, answer)
	}
	backend := "qwen"
	if len(request.Questions) > 0 {
		backend = request.Questions[0].Fallback
		if backend == "" && len(request.Questions[0].Options) > 0 {
			backend = request.Questions[0].Options[0]
		}
	}
	result.Decision = &Decision{BackendCandidates: []string{backend}, TaskMode: "inspect", Risk: RiskLow, TimeoutHintSeconds: 900, IdleTimeoutEnabled: true, Retry: RetryHint{MaxAttempts: 1}, ExecutionTarget: ExecutionExternal, Confidence: 0, Fallback: true, ModelVersion: e.ModelVersion}
	return result, nil
}
