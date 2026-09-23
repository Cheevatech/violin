package laya

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type FeedbackEvent struct {
	At               time.Time `json:"at"`
	AgentID          string    `json:"agent_id"`
	RequestedBackend string    `json:"requested_backend"`
	SelectedBackend  string    `json:"selected_backend"`
	Mode             string    `json:"mode"`
	LayaMode         string    `json:"laya_mode"`
	ModelVersion     string    `json:"model_version,omitempty"`
	Fallback         bool      `json:"fallback"`
	Confidence       float64   `json:"confidence"`
	Risk             Risk      `json:"risk,omitempty"`
	CostTier         Tier      `json:"cost_tier,omitempty"`
	LatencyTier      Tier      `json:"latency_tier,omitempty"`
	Outcome          string    `json:"outcome"`
	DurationSeconds  float64   `json:"duration_seconds,omitempty"`
	TimeoutSeconds   int       `json:"timeout_seconds"`
	ErrorClass       string    `json:"error_class,omitempty"`
	Corrected        bool      `json:"corrected,omitempty"`
	CorrectedBackend string    `json:"corrected_backend,omitempty"`
	CorrectedMode    string    `json:"corrected_mode,omitempty"`
	CorrectedRisk    Risk      `json:"corrected_risk,omitempty"`
	CorrectedTimeout string    `json:"corrected_timeout_policy,omitempty"`
	CorrectedRetry   string    `json:"corrected_retry_policy,omitempty"`
}

func FeedbackHasAgent(root, agentID string) (bool, error) {
	file, err := os.Open(filepath.Join(root, "laya-feedback.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var event FeedbackEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.AgentID == agentID {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func AppendFeedback(root string, event FeedbackEvent) error {
	if root == "" {
		return os.ErrInvalid
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(root, "laya-feedback.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}
