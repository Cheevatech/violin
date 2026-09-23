package jobs

import (
	"encoding/json"
	"github.com/film/violin/internal/laya"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRejectsInvalidDescriptors(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root, "../other"); err == nil {
		t.Fatal("accepted traversal")
	}
	if _, err := Open(root, "123-0"); err == nil {
		t.Fatal("accepted zero PID")
	}
	run := filepath.Join(root, "qwen-123")
	if err := os.MkdirAll(run, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "jobs"), 0700); err != nil {
		t.Fatal(err)
	}
	d := Descriptor{AgentID: "123-456", PID: 456, Backend: "qwen", Evidence: run, Output: filepath.Join(run, "output.json"), Status: filepath.Join(run, "status.json"), TaskFile: filepath.Join(run, "task.txt")}
	data, _ := json.Marshal(d)
	path := filepath.Join(root, "jobs", "123-456.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, "123-456"); err != nil {
		t.Fatal(err)
	}
	d.Output = "/tmp/other"
	data, _ = json.Marshal(d)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, "123-456"); err == nil {
		t.Fatal("accepted escaped output")
	}
}

func TestActiveLayaRequiresReviewForRiskUncertaintyFallbackAndHighRisk(t *testing.T) {
	decision := &laya.Decision{Risk: laya.RiskLow, HeadConfidence: map[string]float64{"risk": .91, "task_mode": .9}, HeadMargin: map[string]float64{"risk": .8, "task_mode": .7}}
	if got := ReviewReason(false, decision, "auto", false); got != "" {
		t.Fatalf("confident low-risk decision requires review: %s", got)
	}
	decision.HeadConfidence["risk"] = .79
	if got := ReviewReason(false, decision, "inspect", false); got != "risk_uncertain" {
		t.Fatalf("uncertain risk reason=%q", got)
	}
	decision.HeadConfidence["risk"] = .91
	decision.Risk = laya.RiskHigh
	if got := ReviewReason(false, decision, "inspect", false); got != "high_risk" {
		t.Fatalf("high risk reason=%q", got)
	}
	if got := ReviewReason(true, decision, "inspect", false); got != "laya_fallback_or_missing_decision" {
		t.Fatalf("fallback reason=%q", got)
	}
	if got := ReviewReason(true, decision, "inspect", true); got != "" {
		t.Fatalf("reviewed decision remains blocked: %q", got)
	}
	decision.Risk = laya.RiskLow
	decision.HeadConfidence["risk"] = .91
	decision.HeadMargin["risk"] = .8
	decision.HeadConfidence["task_mode"] = .79
	if got := ReviewReason(false, decision, "auto", false); got != "mode_uncertain" {
		t.Fatalf("mode uncertainty reason=%q", got)
	}
}

func TestInterruptDoesNotSignalUnverifiedPID(t *testing.T) {
	j := &Job{Descriptor: Descriptor{PID: os.Getpid(), TaskFile: "/impossible/task.txt"}}
	if _, err := j.Interrupt(); err == nil {
		t.Fatal("accepted unrelated PID")
	}
}
