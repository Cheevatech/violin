package laya

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type CalibrationReport struct {
	Events    int            `json:"events"`
	Completed int            `json:"completed"`
	Failed    int            `json:"failed"`
	ByBackend map[string]int `json:"by_backend"`
	Candidate string         `json:"candidate"`
	Promoted  bool           `json:"promoted"`
}

func Recalibrate(root string) (CalibrationReport, error) {
	if root == "" {
		return CalibrationReport{}, errors.New("feedback root is required")
	}
	report := CalibrationReport{ByBackend: map[string]int{}, Candidate: "calibration-candidate", Promoted: false}
	file, err := os.Open(filepath.Join(root, "laya-feedback.jsonl"))
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event FeedbackEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		report.Events++
		report.ByBackend[event.SelectedBackend]++
		if event.Outcome == "completed" {
			report.Completed++
		} else {
			report.Failed++
		}
	}
	if err := scanner.Err(); err != nil {
		return report, err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	if err := os.WriteFile(filepath.Join(root, "calibration-candidate.json"), append(data, '\n'), 0600); err != nil {
		return report, err
	}
	return report, nil
}
