package laya

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendFeedbackStoresMetadataWithoutTaskText(t *testing.T) {
	root := t.TempDir()
	if err := AppendFeedback(root, FeedbackEvent{AgentID: "job-1", SelectedBackend: "qwen", Outcome: "completed"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "laya-feedback.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "job-1") || strings.Contains(string(data), "task") {
		t.Fatalf("unexpected feedback=%s", data)
	}
	if mode := (func() os.FileMode {
		info, _ := os.Stat(filepath.Join(root, "laya-feedback.jsonl"))
		return info.Mode().Perm()
	})(); mode != 0600 {
		t.Fatalf("feedback permissions=%o", mode)
	}
}

func TestRecalibrateWritesCandidateWithoutPromotion(t *testing.T) {
	root := t.TempDir()
	if err := AppendFeedback(root, FeedbackEvent{SelectedBackend: "qwen", Outcome: "completed"}); err != nil {
		t.Fatal(err)
	}
	report, err := Recalibrate(root)
	if err != nil || report.Events != 1 || report.Completed != 1 || report.Promoted {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if _, err := os.Stat(filepath.Join(root, "calibration-candidate.json")); err != nil {
		t.Fatal(err)
	}
}
