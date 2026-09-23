package laya

import (
	"strconv"
	"testing"
)

func TestTrainingRequiresExplicitSplitsAndProducesMeasuredCandidate(t *testing.T) {
	var examples []LabeledExample
	backends := []string{"qwen", "agy", "claude"}
	modes := []string{"inspect", "implement"}
	risks := []Risk{RiskLow, RiskMedium, RiskHigh}
	for i := 0; i < 90; i++ {
		for b, backend := range backends {
			for m, mode := range modes {
				for r, risk := range risks {
					timeoutPolicy := []string{"short", "standard", "long"}[r]
					retryPolicy := []string{"inspect_once", "never", "never"}[b]
					task := []string{"qwen", "agy", "claude"}[b] + " " + []string{"inspect", "implement"}[m] + " " + string([]Risk{RiskLow, RiskMedium, RiskHigh}[r]) + " " + timeoutPolicy + " " + retryPolicy
					examples = append(examples, LabeledExample{Task: task, Language: "en", Backend: backend, Mode: mode, Risk: risk, TimeoutPolicy: timeoutPolicy, RetryPolicy: retryPolicy, Split: "train", Reviewed: true})
				}
			}
		}
	}
	for i := 0; i < 100; i++ {
		for b, backend := range backends {
			for m, mode := range modes {
				for r, risk := range risks {
					timeoutPolicy := []string{"short", "standard", "long"}[r]
					retryPolicy := []string{"inspect_once", "never", "never"}[b]
					task := []string{"qwen", "agy", "claude"}[b] + " " + []string{"inspect", "implement"}[m] + " " + string([]Risk{RiskLow, RiskMedium, RiskHigh}[r]) + " " + timeoutPolicy + " " + retryPolicy + " holdout " + strconv.Itoa(i)
					examples = append(examples, LabeledExample{Task: task, Language: "en", Backend: backend, Mode: mode, Risk: risk, TimeoutPolicy: timeoutPolicy, RetryPolicy: retryPolicy, Split: "holdout", Reviewed: true})
				}
			}
		}
	}
	model, report, err := TrainDataset(examples, "test-candidate")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Validate(); err != nil {
		t.Fatal(err)
	}
	decision, err := model.Evaluate(Request{Language: "en", State: map[string]any{"task": "qwen inspect low short inspect_once"}})
	if err != nil || decision.Decision == nil || decision.Decision.HeadConfidence["timeout_policy"] < .8 || decision.Decision.HeadConfidence["retry_policy"] < .8 {
		t.Fatalf("trained policy heads not available: result=%+v err=%v", decision, err)
	}
	if report.HoldoutExamples != 1800 || report.HighRiskRecall != 1 || !report.MeetsGate {
		t.Fatalf("unexpected training report: %+v", report)
	}
}

func TestLabeledExampleRejectsUnreviewedOrInvalidSplit(t *testing.T) {
	if err := (LabeledExample{Task: "fix", Language: "en", Backend: "qwen", Mode: "inspect", Risk: RiskLow, TimeoutPolicy: "standard", RetryPolicy: "never", Split: "train"}).Validate(); err == nil {
		t.Fatal("accepted missing split")
	}
	item := LabeledExample{Task: "fix", Language: "fr", Backend: "qwen", Mode: "inspect", Risk: RiskLow, TimeoutPolicy: "standard", RetryPolicy: "never", Split: "train", Reviewed: true}
	if err := item.Validate(); err == nil {
		t.Fatal("accepted non-English dataset example")
	}
}

func TestTrainDatasetRejectsHoldoutLeakage(t *testing.T) {
	items := []LabeledExample{
		{Task: "same task", Language: "en", Backend: "qwen", Mode: "inspect", Risk: RiskLow, TimeoutPolicy: "standard", RetryPolicy: "never", Split: "train", Reviewed: true},
		{Task: "same task", Language: "en", Backend: "qwen", Mode: "inspect", Risk: RiskLow, TimeoutPolicy: "standard", RetryPolicy: "never", Split: "holdout", Reviewed: true},
	}
	if _, _, err := TrainDataset(items, "candidate"); err == nil {
		t.Fatal("accepted task duplicated across train and holdout")
	}
}
