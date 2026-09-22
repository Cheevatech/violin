package laya

import (
	"errors"
	"fmt"
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
type Result struct {
	Answers      []Answer `json:"answers"`
	ModelVersion string   `json:"model_version"`
	LatencyMS    float64  `json:"latency_ms"`
	Fallback     bool     `json:"fallback"`
	Error        string   `json:"error,omitempty"`
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
	return result, nil
}
