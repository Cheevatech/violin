package jobs

import (
	"encoding/json"
	"github.com/film/violin/internal/laya"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestTimedOutJobStaysVisibleAndCanResumeFromItsCheckpoint(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_WORKER_RUNS", filepath.Join(root, "runs"))
	t.Setenv("VIOLIN_TEST_JOB_WORKER", "1")
	t.Setenv("VIOLIN_LAYA_MODE", "shadow")
	t.Setenv("VIOLIN_AGY_TRANSPORT", "")
	t.Setenv("VIOLIN_AGY_CLI_COMMAND", "")
	configPath := filepath.Join(root, "config.toml")
	config := "[laya]\nmode = \"shadow\"\n[backend.agy]\ncommand = [\"fixture\"]\nprotocol = \"agy\"\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)

	job, err := Spawn(Options{Root: os.Getenv("VIOLIN_WORKER_RUNS"), Workspace: workspace,
		Backend: "agy", RequestedBackend: "agy", Mode: "implement", Task: "Implement the feature", Timeout: 30})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	first, err := job.Wait(10)
	firstReport, ok := first.(map[string]any)
	if err != nil || !ok || firstReport["status"] != "timeout" || firstReport["resumable"] != true {
		t.Fatalf("timeout report=%v err=%v", first, err)
	}
	if _, err := Open(os.Getenv("VIOLIN_WORKER_RUNS"), job.Descriptor.AgentID); err != nil {
		t.Fatalf("timed out job descriptor was not retained: %v", err)
	}
	listed, err := List(os.Getenv("VIOLIN_WORKER_RUNS"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range listed {
		if item["agent_id"] == job.Descriptor.AgentID && item["resumable"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("timed out job missing from list: %v", listed)
	}

	resumed, err := Resume(os.Getenv("VIOLIN_WORKER_RUNS"), job.Descriptor.AgentID, 60)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(resumed.Descriptor.TaskFile)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := string(data)
	for _, expected := range []string{"Implement the feature", "partial implementation", "partial.go", "Resume the unfinished task"} {
		if !strings.Contains(checkpoint, expected) {
			t.Fatalf("resume task missing %q: %s", expected, checkpoint)
		}
	}
	if resumed.Descriptor.ParentAgentID != job.Descriptor.AgentID || resumed.Descriptor.Attempt != 2 {
		t.Fatalf("resume lineage missing: %+v", resumed.Descriptor)
	}
	time.Sleep(50 * time.Millisecond)
	second, err := resumed.Wait(10)
	secondReport, ok := second.(map[string]any)
	if err != nil || !ok || secondReport["status"] != "completed" || secondReport["resumed_from"] != job.Descriptor.AgentID {
		t.Fatalf("resumed report=%v err=%v", second, err)
	}
	previous, err := Open(os.Getenv("VIOLIN_WORKER_RUNS"), job.Descriptor.AgentID)
	if err != nil || previous.Descriptor.ResumedBy != resumed.Descriptor.AgentID {
		t.Fatalf("previous attempt lineage=%+v err=%v", previous, err)
	}
	if _, err := Resume(os.Getenv("VIOLIN_WORKER_RUNS"), job.Descriptor.AgentID, 60); err == nil {
		t.Fatal("resumed the same timed out attempt more than once")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("VIOLIN_TEST_JOB_WORKER") == "1" {
		taskPath := ""
		for index := 1; index+1 < len(os.Args); index++ {
			if os.Args[index] == "--task-file" {
				taskPath = os.Args[index+1]
				break
			}
		}
		task, _ := os.ReadFile(taskPath)
		if strings.Contains(string(task), "Resume the unfinished task") {
			_, _ = os.Stdout.Write([]byte("{\"status\":\"completed\",\"summary\":\"finished remaining work\",\"final_message_seen\":true}\n"))
		} else {
			_, _ = os.Stdout.Write([]byte("{\"status\":\"timeout\",\"summary\":\"partial implementation\",\"changed_files\":[\"partial.go\"],\"final_message_seen\":false}\n"))
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
