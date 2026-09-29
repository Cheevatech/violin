package jobs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
	"github.com/film/violin/internal/laya"
)

type confidentLowRiskEngine struct{}

func (confidentLowRiskEngine) Evaluate(laya.Request) (laya.Result, error) {
	return laya.Result{
		ModelVersion: "test-low-risk",
		Decision: &laya.Decision{
			BackendCandidates: []string{"agy"}, TaskMode: "implement", Risk: laya.RiskLow,
			TimeoutHintSeconds: 900, IdleTimeoutEnabled: true, Retry: laya.RetryHint{MaxAttempts: 1},
			ExecutionTarget: laya.ExecutionExternal, CostTier: laya.TierMedium, LatencyTier: laya.TierMedium,
			Confidence: .99, Margin: .9, HeadConfidence: map[string]float64{"risk": .99, "task_mode": .99},
			HeadMargin: map[string]float64{"risk": .9, "task_mode": .9}, ModelVersion: "test-low-risk",
		},
	}, nil
}

func TestActiveLayaRequiresReviewForRiskyOperationsEvenWhenModelSaysLow(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("[laya]\nmode = \"active\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)
	t.Setenv("VIOLIN_WORKER_RUNS", filepath.Join(root, "runs"))
	t.Setenv("VIOLIN_LAYA_MODE", "")
	SetLocalLayaEngine(confidentLowRiskEngine{})
	t.Cleanup(func() { SetLocalLayaEngine(nil) })

	cases := []struct {
		name string
		task string
		mode string
	}{
		{"delete production database", "Delete every customer record in the production database now; this is irreversible and there is no backup.", "implement"},
		{"rotate live signing keys", "Rotate the live production signing keys immediately and invalidate all existing sessions.", "implement"},
		{"production authentication deploy", "Deploy the new authentication middleware to production and disable the old access checks.", "implement"},
		{"production database migration", "Migrate the production payments database and drop the legacy table as part of the same change.", "implement"},
		{"revoke production credentials", "Revoke the exposed production API credentials and force every customer integration to re-authenticate.", "implement"},
		{"publish cryptographic change", "Publish the package release that changes cryptographic verification used by all downstream production clients.", "implement"},
		{"delete production storage", "Delete the production cloud storage bucket and all objects because the service is being retired.", "implement"},
		{"rewrite all customer permissions", "Run this script against every customer production environment to rewrite account permissions.", "auto"},
		{"remove production security controls", "Remove the production firewall and TLS restrictions to restore connectivity quickly.", "implement"},
		{"purge customer accounts", "Permanently purge the requested customer accounts and all associated billing records.", "implement"},
		{"live customer charges", "Change the transaction fee charged to all live customers starting immediately.", "implement"},
		{"untested production writes", "Deploy this untested database write path to production during active payment processing.", "implement"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Spawn(Options{Root: filepath.Join(root, tc.name), Workspace: workspace, Backend: "agy", Mode: tc.mode, Task: tc.task, Timeout: 30})
			var review ReviewRequiredError
			if !errors.As(err, &review) || review.Reason != "high_risk_operation_detected" {
				t.Fatalf("Spawn error=%v, want high_risk_operation_detected review", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, tc.name, "scheduler.lock")); !os.IsNotExist(statErr) {
				t.Fatalf("scheduler state was created before review: stat err=%v", statErr)
			}
		})
	}
}

func TestActiveLayaRequiresReviewForNonLatinImplementation(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("[laya]\nmode = \"active\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)
	t.Setenv("VIOLIN_WORKER_RUNS", filepath.Join(root, "runs"))
	t.Setenv("VIOLIN_LAYA_MODE", "")
	SetLocalLayaEngine(confidentLowRiskEngine{})
	t.Cleanup(func() { SetLocalLayaEngine(nil) })

	_, err := Spawn(Options{Root: filepath.Join(root, "job"), Workspace: workspace, Backend: "agy", Mode: "implement", Task: "ลบฐานข้อมูล production และข้อมูลลูกค้าทั้งหมด", Timeout: 30})
	var review ReviewRequiredError
	if !errors.As(err, &review) || review.Reason != "non_latin_implementation_requires_review" {
		t.Fatalf("Spawn error=%v, want non_latin_implementation_requires_review", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "job", "scheduler.lock")); !os.IsNotExist(statErr) {
		t.Fatalf("scheduler state was created before review: stat err=%v", statErr)
	}
}

func TestActiveLayaDoesNotTreatRoutineSecurityDocumentationAsHighRisk(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("[laya]\nmode = \"active\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)
	t.Setenv("VIOLIN_WORKER_RUNS", filepath.Join(root, "runs"))
	t.Setenv("VIOLIN_LAYA_MODE", "")
	SetLocalLayaEngine(confidentLowRiskEngine{})
	t.Cleanup(func() { SetLocalLayaEngine(nil) })

	_, err := Spawn(Options{Root: filepath.Join(root, "job"), Workspace: workspace, Backend: "agy", Mode: "implement", Task: "Update docs about authentication middleware and API key setup.", Timeout: 14401})
	var review ReviewRequiredError
	if errors.As(err, &review) {
		t.Fatalf("routine documentation unexpectedly required high-risk review: %s", review.Reason)
	}
	if err == nil {
		t.Fatal("expected timeout validation to stop before worker startup")
	}
}

func TestLayaFallbackUsesConfiguredBackendForAutoRequest(t *testing.T) {
	for _, mode := range []string{"auto", "inspect", "implement"} {
		result := EvaluateLaya(t.TempDir(), "inspect the code", mode, "auto", config.Config{})
		if result.Decision == nil {
			t.Fatalf("mode %s fallback has no decision: %+v", mode, result)
		}
		if got := result.Decision.BackendCandidates[0]; got != "agy" {
			t.Fatalf("mode %s auto fallback backend=%q, want first configured option agy", mode, got)
		}
		wantMode := mode
		if wantMode == "auto" {
			wantMode = "inspect"
		}
		if result.Decision.TaskMode != wantMode {
			t.Fatalf("mode %s fallback task mode=%q, want %q", mode, result.Decision.TaskMode, wantMode)
		}
		if result.Decision.Risk != laya.RiskMedium {
			t.Fatalf("mode %s fallback risk=%q, want request fallback medium", mode, result.Decision.Risk)
		}
		if err := result.Decision.Validate(); err != nil {
			t.Fatalf("mode %s fallback returned an invalid decision: %+v: %v", mode, result.Decision, err)
		}
	}
}

func TestAutoSkipsUnhealthyQwenAndReportsFallback(t *testing.T) {
	_, workspace := configureJobRoutingTest(t, "#!/bin/sh\nprintf '{\\\"status\\\":\\\"qwen_unhealthy\\\"}\\n'\nexit 79\n")
	job, err := Spawn(Options{Root: os.Getenv("VIOLIN_WORKER_RUNS"), Workspace: workspace, Backend: "auto", Mode: "inspect", Task: "inspect", Timeout: 30})
	if err != nil {
		t.Fatal(err)
	}
	if job.Descriptor.Backend != "agy" || job.Descriptor.FallbackReason != "qwen_health_qwen_unhealthy" || job.Descriptor.HealthStatus != "qwen_unhealthy" {
		t.Fatalf("auto fallback descriptor = %+v", job.Descriptor)
	}
	live := job.Live()
	if live["fallback_reason"] != "qwen_health_qwen_unhealthy" || live["health_status"] != "qwen_unhealthy" {
		t.Fatalf("live report omitted fallback evidence: %#v", live)
	}
	finished, err := job.Wait(1)
	if err != nil {
		t.Fatal(err)
	}
	if report, ok := finished.(map[string]any); !ok || report["fallback_reason"] != "qwen_health_qwen_unhealthy" || report["health_status"] != "qwen_unhealthy" {
		t.Fatalf("finished report omitted fallback evidence: %#v", finished)
	}
}

func TestAutoAcceptsHealthyQwenAndAdvancesRoundRobinOnce(t *testing.T) {
	_, workspace := configureJobRoutingTest(t, "#!/bin/sh\nprintf '{\\\"status\\\":\\\"ready\\\"}\\n'\n")
	job, err := Spawn(Options{Root: os.Getenv("VIOLIN_WORKER_RUNS"), Workspace: workspace, Backend: "auto", Mode: "inspect", Task: "inspect", Timeout: 30})
	if err != nil {
		t.Fatal(err)
	}
	if job.Descriptor.Backend != "qwen" || job.Descriptor.HealthStatus != "healthy" || job.Descriptor.FallbackReason != "" {
		t.Fatalf("healthy Qwen selection = %+v", job.Descriptor)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("VIOLIN_WORKER_RUNS"), "round-robin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]int
	if err := json.Unmarshal(data, &state); err != nil || state["index"] != 1 {
		t.Fatalf("round-robin state=%s err=%v, want one advance to index 1", data, err)
	}
	_, _ = job.Interrupt()
}

func TestExplicitQwenRequestDoesNotHealthFallback(t *testing.T) {
	_, workspace := configureJobRoutingTest(t, "#!/bin/sh\nprintf '{\\\"status\\\":\\\"qwen_unhealthy\\\"}\\n'\nexit 79\n")
	job, err := Spawn(Options{Root: os.Getenv("VIOLIN_WORKER_RUNS"), Workspace: workspace, Backend: "qwen", Mode: "inspect", Task: "inspect", Timeout: 30})
	if err != nil {
		t.Fatal(err)
	}
	if job.Descriptor.Backend != "qwen" || job.Descriptor.HealthStatus != "" || job.Descriptor.FallbackReason != "" {
		t.Fatalf("explicit Qwen request was changed by auto health routing: %+v", job.Descriptor)
	}
	_, _ = job.Interrupt()
}

func TestQwenWorkerEnvironmentLoadsLLMuxKeyWithoutPersistingIt(t *testing.T) {
	t.Setenv("LLMUX_API_KEY", "")
	store := credentials.Store{Env: map[string]string{"LLMUX_API_KEY": "worker-test-key"}}
	for _, entry := range workerEnv(config.Defaults(), "qwen", "", "", "", store) {
		if entry == "LLMUX_API_KEY=worker-test-key" {
			return
		}
	}
	t.Fatal("Qwen worker environment omitted LLMux Keychain credential")
}

func TestAutoFallbackCapacityErrorRetainsQwenHealthFailure(t *testing.T) {
	root, workspace := configureJobRoutingTest(t, "#!/bin/sh\nprintf '{\\\"status\\\":\\\"qwen_unhealthy\\\"}\\n'\nexit 79\n")
	configPath := os.Getenv("VIOLIN_CONFIG")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, append(data, []byte("max_concurrency = 1\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(root, "violin-worker")
	if err := os.WriteFile(worker, []byte("#!/bin/sh\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_TEST_JOB_WORKER", "")
	t.Setenv("VIOLIN_WORKER_BIN", worker)
	first, err := Spawn(Options{Root: os.Getenv("VIOLIN_WORKER_RUNS"), Workspace: workspace, Backend: "agy", Mode: "inspect", Task: "occupy AGY", Timeout: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Interrupt()
	_, err = Spawn(Options{Root: os.Getenv("VIOLIN_WORKER_RUNS"), Workspace: workspace, Backend: "auto", Mode: "inspect", Task: "inspect", Timeout: 30})
	if err == nil || !strings.Contains(err.Error(), "Qwen health check failed (qwen_unhealthy); fallback backend unavailable") {
		t.Fatalf("fallback capacity error lost Qwen health evidence: %v", err)
	}
}

func configureJobRoutingTest(t *testing.T, healthScript string) (string, string) {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	healthCommand := filepath.Join(root, "qwen-health")
	if err := os.WriteFile(healthCommand, []byte(healthScript), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	config := "[laya]\nmode = \"shadow\"\n[scheduler]\norder = [\"qwen\", \"agy\"]\n[backend.qwen]\ncommand = [\"fixture\"]\nprotocol = \"qwen\"\nhealth_command = [\"" + healthCommand + "\"]\n[backend.agy]\ncommand = [\"fixture\"]\nprotocol = \"agy\"\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)
	t.Setenv("VIOLIN_WORKER_RUNS", filepath.Join(root, "runs"))
	t.Setenv("VIOLIN_TEST_JOB_WORKER", "1")
	t.Setenv("VIOLIN_LAYA_MODE", "shadow")
	return root, workspace
}

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
