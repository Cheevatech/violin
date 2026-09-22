package laya

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
)

type Model struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	Version       string          `json:"version"`
	Language      string          `json:"language"`
	Heads         map[string]Head `json:"heads"`
}

type Head struct {
	Labels  []string             `json:"labels"`
	Bias    []float64            `json:"bias"`
	Weights map[string][]float64 `json:"weights"`
}

var featurePattern = regexp.MustCompile(`[a-z0-9]+`)

func LoadModel(path string) (Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Model{}, err
	}
	var model Model
	if err := json.Unmarshal(data, &model); err != nil {
		return Model{}, err
	}
	if err := model.Validate(); err != nil {
		return Model{}, err
	}
	return model, nil
}

func (m Model) Validate() error {
	if m.SchemaVersion != 1 || m.ID == "" || m.Version == "" || strings.ToLower(m.Language) != ProtocolLanguage {
		return errors.New("invalid English Laya model metadata")
	}
	if len(m.Heads) == 0 {
		return errors.New("Laya model has no heads")
	}
	for name, head := range m.Heads {
		if len(head.Labels) < 2 || len(head.Bias) != len(head.Labels) {
			return fmt.Errorf("invalid Laya model head %q", name)
		}
		for feature, weights := range head.Weights {
			if feature == "" || len(weights) != len(head.Labels) {
				return fmt.Errorf("invalid Laya feature %q in head %q", feature, name)
			}
		}
	}
	return nil
}

func (m Model) Evaluate(request Request) (Result, error) {
	if err := m.Validate(); err != nil {
		return Result{}, err
	}
	if strings.ToLower(strings.TrimSpace(request.Language)) != ProtocolLanguage {
		return Result{}, fmt.Errorf("unsupported Laya protocol language %q: Violin requires English (en)", request.Language)
	}
	text := ""
	if state, ok := request.State.(map[string]any); ok {
		text, _ = state["task"].(string)
	}
	features := extractFeatures(text)
	backend := m.predict("backend", features)
	mode := m.predict("task_mode", features)
	risk := m.predict("risk", features)
	if backend == nil || mode == nil || risk == nil {
		return Result{}, errors.New("Laya model is missing a required decision head")
	}
	decision := Decision{
		BackendCandidates:  backend.labels,
		TaskMode:           mode.label,
		Risk:               Risk(risk.label),
		TimeoutHintSeconds: timeoutHint(mode.label, risk.label),
		IdleTimeoutEnabled: backend.label == "claude",
		Retry:              RetryHint{MaxAttempts: 1},
		ExecutionTarget:    ExecutionExternal,
		CostTier:           costTier(backend.label),
		LatencyTier:        latencyTier(backend.label),
		Confidence:         backend.confidence,
		Margin:             backend.margin,
		ReasonCodes:        reasonCodes(features, backend.label, mode.label, risk.label),
		ModelVersion:       m.Version,
	}
	if err := decision.Validate(); err != nil {
		return Result{}, err
	}
	return Result{Decision: &decision, ModelVersion: m.Version}, nil
}

func costTier(backend string) Tier {
	if backend == "claude" {
		return TierHigh
	}
	return TierMedium
}

func latencyTier(backend string) Tier {
	if backend == "qwen" {
		return TierMedium
	}
	return TierLow
}

type prediction struct {
	label      string
	labels     []string
	confidence float64
	margin     float64
}

func (m Model) predict(name string, features map[string]float64) *prediction {
	head, ok := m.Heads[name]
	if !ok {
		return nil
	}
	scores := append([]float64(nil), head.Bias...)
	for feature, value := range features {
		for index, weight := range head.Weights[feature] {
			scores[index] += value * weight
		}
	}
	probabilities := softmax(scores)
	order := make([]int, len(head.Labels))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return probabilities[order[i]] > probabilities[order[j]] })
	best := order[0]
	second := 0.0
	if len(order) > 1 {
		second = probabilities[order[1]]
	}
	labels := make([]string, len(order))
	for i, index := range order {
		labels[i] = head.Labels[index]
	}
	return &prediction{label: head.Labels[best], labels: labels, confidence: probabilities[best], margin: probabilities[best] - second}
}

func softmax(scores []float64) []float64 {
	maximum := scores[0]
	for _, score := range scores[1:] {
		if score > maximum {
			maximum = score
		}
	}
	total := 0.0
	result := make([]float64, len(scores))
	for i, score := range scores {
		result[i] = math.Exp(score - maximum)
		total += result[i]
	}
	for i := range result {
		result[i] /= total
	}
	return result
}

func extractFeatures(text string) map[string]float64 {
	words := featurePattern.FindAllString(strings.ToLower(text), -1)
	features := map[string]float64{}
	for _, word := range words {
		features[word]++
	}
	for i := 0; i+1 < len(words); i++ {
		features[words[i]+"_"+words[i+1]] += 1.5
	}
	return features
}

func timeoutHint(mode, risk string) int {
	if mode == "implement" || risk == string(RiskHigh) {
		return 3600
	}
	return 900
}

func reasonCodes(features map[string]float64, backend, mode, risk string) []string {
	reasons := []string{"model_backend_" + backend, "model_mode_" + mode, "model_risk_" + risk}
	if features["fix"] > 0 || features["implement"] > 0 || features["change"] > 0 {
		reasons = append(reasons, "task_requests_change")
	}
	if features["security"] > 0 || features["credential"] > 0 || features["secret"] > 0 {
		reasons = append(reasons, "task_mentions_sensitive_data")
	}
	return reasons
}
