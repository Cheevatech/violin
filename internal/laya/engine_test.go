package laya

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/film/violin/internal/models"
)

func TestFallbackEngineReturnsTypedAnswers(t *testing.T) {
	result, err := (FallbackEngine{ModelVersion: "fallback"}).Evaluate(Request{Questions: []Question{{ID: "backend", Kind: Choice, Options: []string{"agy", "qwen"}, Fallback: "qwen"}, {ID: "safe", Kind: Noul}}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Fallback || len(result.Answers) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Answers[0].Value != "qwen" {
		t.Fatalf("unexpected choice: %+v", result.Answers[0])
	}
}

func TestFallbackEngineRejectsNonEnglishProtocol(t *testing.T) {
	_, err := (FallbackEngine{}).Evaluate(Request{Language: "th", Questions: []Question{{ID: "backend", Kind: Choice, Options: []string{"qwen"}}}})
	if err == nil {
		t.Fatal("expected non-English protocol rejection")
	}
}

func TestManagedEngineUsesVerifiedModelRunnerAndKeepsModelPathSeparate(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("model")
	if err := os.WriteFile(filepath.Join(source, "model.bin"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	manager, err := models.NewManager(filepath.Join(root, "models"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Install(models.Manifest{ID: models.DefaultModelID, Language: models.DefaultLanguage, Version: "v1", Runtime: "fixture", Artifacts: []models.Artifact{{Path: "model.bin", SHA256: hex.EncodeToString(hash[:])}}}, source, true); err != nil {
		t.Fatal(err)
	}
	runner := []string{"/bin/sh", "-c", "test -n \"$VIOLIN_LAYA_MODEL_DIR\" && printf '%s' '{\"model_version\":\"v1\",\"answers\":[{\"id\":\"backend\",\"kind\":\"choice\",\"value\":\"qwen\",\"confidence\":0.9,\"fallback\":false}]}'"}
	result, err := (ManagedEngine{Manager: manager, Runner: runner, Fallback: FallbackEngine{}}).Evaluate(Request{Language: ProtocolLanguage, Questions: []Question{{ID: "backend", Kind: Choice, Options: []string{"qwen"}}}})
	if err != nil || result.Fallback || result.ModelVersion != "v1" || len(result.Answers) != 1 || result.Decision == nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestManagedEngineRejectsInvalidDecision(t *testing.T) {
	result, err := (ManagedEngine{Runner: []string{"/bin/sh", "-c", "printf '%s' '{\"decision\":{\"backend_candidates\":[\"unknown\"],\"task_mode\":\"inspect\",\"risk\":\"low\",\"timeout_hint_seconds\":900,\"retry_hint\":{\"max_attempts\":1},\"execution_target\":\"external\",\"confidence\":0,\"margin\":0}}'"}, Fallback: FallbackEngine{}}).Evaluate(Request{Language: ProtocolLanguage, Questions: []Question{{ID: "backend", Kind: Choice, Options: []string{"qwen"}, Fallback: "qwen"}}})
	if err != nil || !result.Fallback || !strings.Contains(result.Error, "unsupported Laya backend") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRunnerFromEnvRequiresJSONArgv(t *testing.T) {
	t.Setenv("VIOLIN_LAYA_RUNNER", `["runner","--json"]`)
	runner := RunnerFromEnv()
	if len(runner) != 2 || runner[0] != "runner" {
		t.Fatalf("runner=%v", runner)
	}
	t.Setenv("VIOLIN_LAYA_RUNNER", "runner --json")
	if RunnerFromEnv() != nil {
		t.Fatal("expected non-JSON runner to be rejected")
	}
}

func TestManagedEngineHonorsConfiguredRunnerTimeout(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("model")
	if err := os.WriteFile(filepath.Join(source, "model.bin"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	manager, err := models.NewManager(filepath.Join(root, "models"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Install(models.Manifest{ID: models.DefaultModelID, Language: models.DefaultLanguage, Version: "v1", Artifacts: []models.Artifact{{Path: "model.bin", SHA256: hex.EncodeToString(hash[:])}}}, source, true); err != nil {
		t.Fatal(err)
	}
	result, err := (ManagedEngine{Manager: manager, Runner: []string{"/bin/sh", "-c", "sleep 2"}, Timeout: time.Second, Fallback: FallbackEngine{}}).Evaluate(Request{Language: ProtocolLanguage, Questions: []Question{{ID: "backend", Kind: Choice, Options: []string{"qwen"}}}})
	if err != nil || !result.Fallback || result.Error != "Laya runner timed out" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestManagedEngineUsesBuiltinVerifiedClassifier(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	model := Model{SchemaVersion: 1, ID: models.DefaultModelID, Version: "v1", Language: ProtocolLanguage, Heads: map[string]Head{
		"backend":   {Labels: []string{"qwen", "agy", "claude"}, Bias: []float64{0, 0, 0}, Weights: map[string][]float64{"implement": {2, 0, 0}, "research": {0, 2, 0}, "review": {0, 0, 2}}},
		"task_mode": {Labels: []string{"inspect", "implement"}, Bias: []float64{0, 0}, Weights: map[string][]float64{"implement": {0, 3}, "review": {2, 0}}},
		"risk":      {Labels: []string{"low", "medium", "high"}, Bias: []float64{0, 0, 0}, Weights: map[string][]float64{"security": {0, 0, 3}}},
	}}
	data, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(source, "model.json")
	if err := os.WriteFile(modelPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	manager, err := models.NewManager(filepath.Join(root, "models"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Install(models.Manifest{ID: models.DefaultModelID, Language: models.DefaultLanguage, Version: "v1", Runtime: "builtin-go", Artifacts: []models.Artifact{{Path: "model.json", SHA256: hex.EncodeToString(hash[:])}}}, source, true); err != nil {
		t.Fatal(err)
	}
	result, err := (ManagedEngine{Manager: manager, Fallback: FallbackEngine{}}).Evaluate(Request{Language: ProtocolLanguage, State: map[string]any{"task": "implement the fix"}})
	if err != nil || result.Fallback || result.Decision == nil || result.Decision.BackendCandidates[0] != "qwen" || result.Decision.TaskMode != "implement" || result.Decision.CostTier == "" || result.Decision.LatencyTier == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestEnsureDefaultModelInstallsVerifiedEnglishModel(t *testing.T) {
	root := t.TempDir()
	if err := EnsureDefaultModel(root); err != nil {
		t.Fatal(err)
	}
	manager, err := models.NewManager(filepath.Join(root, "models"))
	if err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if status.Active != DefaultModelVersion || !status.Verified || status.Manifest.Language != ProtocolLanguage {
		t.Fatalf("status=%+v", status)
	}
}
