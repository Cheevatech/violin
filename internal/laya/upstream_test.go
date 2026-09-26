package laya

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeInferenceRuntime struct {
	state   string
	closed  int
	answers []Answer
	err     error
}

func (f *fakeInferenceRuntime) Predict(state string, questions []Question) ([]Answer, string, error) {
	f.state = state
	if f.err != nil {
		return nil, "cpu", f.err
	}
	if f.answers != nil {
		return f.answers, "cpu", nil
	}
	answers := make([]Answer, 0, len(questions))
	for _, q := range questions {
		distributions := map[string][]float64{
			"backend": {0.6, 0.25, 0.15}, "task_mode": {0.7, 0.3},
			"risk": {0.3, 0.6, 0.1}, "timeout_policy": {0.1, 0.8, 0.1},
			"retry_policy": {0.9, 0.1},
		}
		probabilities := map[string]float64{}
		for i, option := range q.Options {
			probabilities[option] = distributions[q.ID][i]
		}
		chosen := q.Options[0]
		for option, probability := range probabilities {
			if probability > probabilities[chosen] {
				chosen = option
			}
		}
		answers = append(answers, Answer{ID: q.ID, Kind: q.Kind, Value: chosen, Probabilities: probabilities, Confidence: probabilities[chosen]})
	}
	return answers, "cpu", nil
}

func (f *fakeInferenceRuntime) Close() error { f.closed++; return nil }

func TestSelectCheckpointEnglishAndThai(t *testing.T) {
	for _, tc := range []struct{ name, state, language, want string }{
		{"english", "Please inspect this Go change", "en", "typed-decisions"},
		{"thai", "ช่วยตรวจโค้ดนี้ให้หน่อย", "en", "multilingual"},
		{"explicit multilingual", "hello", "th", "multilingual"},
		{"empty language", "", "", "typed-decisions"},
		{"French", "Bonjour, pouvez-vous m'aider avec ce problème?", "en", "multilingual"},
		{"Spanish", "Por favor, ayúdame con este problema", "en", "multilingual"},
		{"plain French", "je veux corriger ce problème avec vous", "en", "multilingual"},
		{"plain Spanish", "por favor necesito ayuda con este cambio", "en", "multilingual"},
		{"plain German", "ich brauche bitte hilfe mit diesem problem", "en", "multilingual"},
		{"accented Latin", "¿Cómo puedo resolver este problema?", "", "multilingual"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelectCheckpoint(tc.state, tc.language); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSerializeUpstreamStatePreservesUnicodeAndCompactJSON(t *testing.T) {
	got, err := serializeUpstreamState(map[string]any{"task": "ช่วยตรวจ <ไฟล์>", "mode": "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"task":"ช่วยตรวจ <ไฟล์>","mode":"inspect"}` {
		t.Fatalf("serialized state=%q", got)
	}
}

func TestUpstreamEngineMapsTypedProbabilitiesToDecision(t *testing.T) {
	runtime := &fakeInferenceRuntime{}
	engine := newUpstreamEngine(t.TempDir(), func(_ string, variant string, preferCoreML bool) (InferenceRuntime, string, error) {
		if variant != "typed-decisions" || preferCoreML {
			t.Fatalf("variant=%q preferCoreML=%v", variant, preferCoreML)
		}
		return runtime, "cpu", nil
	})
	defer engine.Close()
	questions := testDecisionQuestions()
	result, err := engine.Evaluate(Request{Language: "en", State: map[string]any{"task": "inspect code"}, Questions: questions})
	if err != nil {
		t.Fatal(err)
	}
	if result.Fallback || result.Decision == nil || result.ModelVersion != "laya-go-onnx/typed-decisions@"+CheckpointRevision {
		t.Fatalf("result=%+v", result)
	}
	if result.Decision.BackendCandidates[0] != "agy" {
		t.Fatalf("candidates=%v", result.Decision.BackendCandidates)
	}
	if result.Decision.TimeoutHintSeconds != 900 || result.Decision.Retry.MaxAttempts != 1 {
		t.Fatalf("decision=%+v", result.Decision)
	}
	if result.Decision.Confidence != .1465 || result.Decision.Margin != .35 {
		t.Fatalf("confidence=%v margin=%v", result.Decision.Confidence, result.Decision.Margin)
	}
	if result.Decision.HeadConfidence["backend"] != .1465 || result.Decision.HeadMargin["backend"] != .35 {
		t.Fatalf("head confidence=%v margin=%v", result.Decision.HeadConfidence, result.Decision.HeadMargin)
	}
	if !strings.Contains(runtime.state, "inspect code") {
		t.Fatalf("state=%q", runtime.state)
	}
}

func TestUpstreamEngineSwitchesAndClosesSingleResidentCheckpoint(t *testing.T) {
	var opened []string
	var runtimes []*fakeInferenceRuntime
	engine := newUpstreamEngine(t.TempDir(), func(_ string, variant string, _ bool) (InferenceRuntime, string, error) {
		opened = append(opened, variant)
		model := &fakeInferenceRuntime{}
		runtimes = append(runtimes, model)
		return model, "cpu", nil
	})
	defer engine.Close()
	if _, err := engine.Evaluate(Request{Language: "en", State: "fix this issue", Questions: testDecisionQuestions()}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Evaluate(Request{Language: "en", State: "ช่วยแก้ปัญหานี้", Questions: testDecisionQuestions()}); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 || opened[0] != "typed-decisions" || opened[1] != "multilingual" {
		t.Fatalf("opened=%v", opened)
	}
	if runtimes[0].closed != 1 || runtimes[1].closed != 0 {
		t.Fatalf("closed=[%d,%d]", runtimes[0].closed, runtimes[1].closed)
	}
}

func TestUpstreamEngineRejectsMalformedTypedOutput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers []Answer
		want    string
	}{
		{"missing head", []Answer{{ID: "backend", Kind: Choice, Value: "agy", Probabilities: map[string]float64{"agy": .6, "qwen": .25, "claude": .15}}}, `omitted "task_mode"`},
		{"empty distribution", []Answer{{ID: "backend", Kind: Choice, Value: "agy", Probabilities: map[string]float64{"agy": .6, "qwen": .25, "claude": .15}}, {ID: "task_mode", Kind: Choice, Value: "inspect", Probabilities: map[string]float64{}}, {ID: "risk", Kind: Choice, Value: "low", Probabilities: map[string]float64{"low": 1}}, {ID: "timeout_policy", Kind: Choice, Value: "standard", Probabilities: map[string]float64{"standard": 1}}, {ID: "retry_policy", Kind: Choice, Value: "no_retry", Probabilities: map[string]float64{"no_retry": 1}}}, `incomplete "task_mode" probabilities`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeInferenceRuntime{answers: tc.answers}
			engine := newUpstreamEngine(t.TempDir(), func(string, string, bool) (InferenceRuntime, string, error) { return fake, "cpu", nil })
			_, err := engine.Evaluate(Request{Language: "en", State: "x", Questions: testDecisionQuestions()})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
			_ = engine.Close()
		})
	}
}

func TestUpstreamEngineReportsLoaderFailure(t *testing.T) {
	engine := newUpstreamEngine(t.TempDir(), func(string, string, bool) (InferenceRuntime, string, error) {
		return nil, "", fmt.Errorf("bad ONNX bundle")
	})
	_, err := engine.Evaluate(Request{Language: "en", State: "x", Questions: testDecisionQuestions()})
	if err == nil || !strings.Contains(err.Error(), "bad ONNX bundle") {
		t.Fatalf("err=%v", err)
	}
}

func TestUpstreamEngineCoreMLRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("VIOLIN_LAYA_COREML", "true")
	called := false
	engine := newUpstreamEngine(t.TempDir(), func(_ string, _ string, preferCoreML bool) (InferenceRuntime, string, error) {
		called = true
		want := runtime.GOOS == "darwin"
		if preferCoreML != want {
			t.Fatalf("preferCoreML=%v want %v", preferCoreML, want)
		}
		return &fakeInferenceRuntime{}, "cpu", nil
	})
	defer engine.Close()
	if _, err := engine.Evaluate(Request{Language: "en", State: "inspect this change", Questions: testDecisionQuestions()}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("runtime loader was not called")
	}
}

func TestEnsureUpstreamDownloadsAndVerifiesBothCheckpoints(t *testing.T) {
	platform, ok := supportedPlatform()
	if !ok {
		t.Skipf("unsupported local platform %s/%s", "test", "test")
	}
	manifest, files := fixtureManifest(platform)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest-"+platform+".json" {
			_ = json.NewEncoder(w).Encode(manifest)
			return
		}
		if body, found := files[strings.TrimPrefix(r.URL.Path, "/")]; found {
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("VIOLIN_LAYA_BUNDLE_BASE_URL", server.URL)
	root := t.TempDir()
	legacy := filepath.Join(root, "laya", "models", "qwen3-4b-q4km-2f3b082")
	legacyCache := filepath.Join(root, "laya", "hf-cache", "hub", "models--ggml-org--Qwen3-4B-GGUF")
	retained := filepath.Join(root, "laya", "models", "qwen-provider-config.json")
	for _, path := range []string{legacy, legacyCache, retained} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureUpstream(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	status := UpstreamRuntimeStatus(root)
	if !status.Installed || status.FallbackInUse || !status.Checkpoints["typed-decisions"] || !status.Checkpoints["multilingual"] {
		t.Fatalf("status=%+v", status)
	}
	if status.Runtime == "" {
		t.Fatalf("status has no runtime path: %+v", status)
	}
	for _, path := range []string{legacy, legacyCache} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("legacy model payload remains at %s", path)
		}
	}
	if _, err := os.Stat(retained); err != nil {
		t.Errorf("provider config was removed: %v", err)
	}
}

func TestEnsureUpstreamRejectsChecksumMismatchAndKeepsFallbackStatus(t *testing.T) {
	platform, ok := supportedPlatform()
	if !ok {
		t.Skip("unsupported platform")
	}
	manifest, files := fixtureManifest(platform)
	first := manifest.Artifacts[0]
	files[first.Asset] = []byte("tampered")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest-"+platform+".json" {
			_ = json.NewEncoder(w).Encode(manifest)
			return
		}
		if body, found := files[strings.TrimPrefix(r.URL.Path, "/")]; found {
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("VIOLIN_LAYA_BUNDLE_BASE_URL", server.URL)
	root := t.TempDir()
	if err := EnsureUpstream(context.Background(), root); err == nil || !strings.Contains(err.Error(), "SHA-256 or size mismatch") {
		t.Fatalf("err=%v", err)
	}
	status := UpstreamRuntimeStatus(root)
	if status.Installed || !status.FallbackInUse {
		t.Fatalf("status=%+v", status)
	}
}

func TestModelStatusDetectsPostInstallTampering(t *testing.T) {
	platform, ok := supportedPlatform()
	if !ok {
		t.Skip("unsupported platform")
	}
	manifest, files := fixtureManifest(platform)
	root := t.TempDir()
	bundleDir := filepath.Join(root, "laya", "onnx")
	for _, artifact := range manifest.Artifacts {
		path := filepath.Join(bundleDir, artifact.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, files[artifact.Asset], 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bundleDir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	status := UpstreamRuntimeStatus(root)
	if !status.Installed {
		t.Fatalf("status=%+v", status)
	}
	if err = os.WriteFile(filepath.Join(bundleDir, manifest.Artifacts[0].Path), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	status = UpstreamRuntimeStatus(root)
	if status.Installed || !status.FallbackInUse || !strings.Contains(status.Error, "integrity") {
		t.Fatalf("status=%+v", status)
	}
}

func TestPlatformSupportMatrix(t *testing.T) {
	for _, tc := range []struct {
		goos, goarch string
		want         bool
	}{
		{"darwin", "arm64", true}, {"darwin", "amd64", false}, {"linux", "arm64", true}, {"linux", "amd64", true},
		{"windows", "amd64", false}, {"linux", "386", false}, {"freebsd", "arm64", false},
	} {
		got, ok := supportedPlatformFor(tc.goos, tc.goarch)
		if ok != tc.want {
			t.Errorf("%s/%s: got %q, supported=%v", tc.goos, tc.goarch, got, ok)
		}
	}
}

func TestPackageUpstreamBundleCreatesChecksumManifest(t *testing.T) {
	platform, ok := supportedPlatform()
	if !ok {
		t.Skip("unsupported local platform")
	}
	parts := strings.Split(platform, "-")
	root, output := t.TempDir(), t.TempDir()
	runtimePath := "runtime/libonnxruntime.so"
	if runtime.GOOS == "darwin" {
		runtimePath = "runtime/libonnxruntime.dylib"
	}
	paths := []string{runtimePath}
	for _, variant := range []string{"typed-decisions", "multilingual"} {
		prefix := "checkpoints/" + variant + "/"
		for _, name := range []string{"laya.onnx", "rl_agent_config.json", "tokenizer/tokenizer.json", "tokenizer/tokenizer_config.json"} {
			paths = append(paths, prefix+name)
		}
	}
	for _, path := range paths {
		target := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("verified:"+path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := PackageUpstreamBundle(root, output, parts[0], parts[1]); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "manifest-"+platform+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest bundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := validateBundleManifest(manifest, platform); err != nil {
		t.Fatal(err)
	}
	tooLarge := manifest
	tooLarge.Artifacts = append([]bundleArtifact(nil), manifest.Artifacts...)
	tooLarge.Artifacts[1].Size = 1 << 40
	if err := validateBundleManifest(tooLarge, platform); err == nil || !strings.Contains(err.Error(), "invalid Laya bundle artifact") {
		t.Fatalf("oversized model artifact accepted: %v", err)
	}
	for _, artifact := range manifest.Artifacts {
		if err := verifyBundleFile(filepath.Join(output, artifact.Asset), artifact); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRealUpstreamInferenceSmoke(t *testing.T) {
	root := os.Getenv("VIOLIN_LAYA_TEST_ROOT")
	assets := os.Getenv("VIOLIN_LAYA_TEST_BUNDLE_ASSETS")
	if assets != "" {
		server := httptest.NewServer(http.FileServer(http.Dir(assets)))
		defer server.Close()
		t.Setenv("VIOLIN_LAYA_BUNDLE_BASE_URL", server.URL)
		root = t.TempDir()
		if err := EnsureUpstream(context.Background(), root); err != nil {
			t.Fatalf("install real bundle: %v", err)
		}
		status := UpstreamRuntimeStatus(root)
		if !status.Installed || status.FallbackInUse {
			t.Fatalf("installed real bundle status=%+v", status)
		}
	} else if root == "" {
		t.Skip("set VIOLIN_LAYA_TEST_ROOT or VIOLIN_LAYA_TEST_BUNDLE_ASSETS to locally staged bundle data")
	}
	engine, err := StartUpstream(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	for _, tc := range []struct{ language, state, checkpoint string }{
		{"en", "Please inspect this Go change and identify the risky parts.", "typed-decisions"},
		{"en", "ช่วยตรวจการเปลี่ยนแปลง Go นี้และหาความเสี่ยง", "multilingual"},
	} {
		result, evalErr := engine.Evaluate(Request{Language: tc.language, State: tc.state, Questions: testDecisionQuestions()})
		if evalErr != nil {
			t.Fatalf("%s inference: %v", tc.checkpoint, evalErr)
		}
		if result.Fallback || result.Decision == nil || !strings.Contains(result.ModelVersion, "/"+tc.checkpoint+"@") {
			t.Fatalf("expected real %s inference, got %+v", tc.checkpoint, result)
		}
		t.Logf("%s device=%s latency=%.1fms decision=%+v", tc.checkpoint, engine.device, result.LatencyMS, result.Decision)
	}
}

func testDecisionQuestions() []Question {
	return []Question{
		{ID: "backend", Kind: Choice, Prompt: "Choose a backend", Options: []string{"agy", "qwen", "claude"}},
		{ID: "task_mode", Kind: Choice, Prompt: "Choose task mode", Options: []string{"inspect", "implement"}},
		{ID: "risk", Kind: Choice, Prompt: "Choose task risk", Options: []string{"low", "medium", "high"}},
		{ID: "timeout_policy", Kind: Choice, Prompt: "Choose timeout", Options: []string{"short", "standard", "long"}},
		{ID: "retry_policy", Kind: Choice, Prompt: "Choose retries", Options: []string{"no_retry", "retry_once"}},
	}
}

func fixtureManifest(platform string) (bundleManifest, map[string][]byte) {
	manifest := bundleManifest{FormatVersion: 1, LayaVersion: UpstreamVersion, CheckpointRevision: CheckpointRevision, ORTVersion: ORTVersion, GOOS: strings.Split(platform, "-")[0], GOARCH: strings.Split(platform, "-")[1]}
	files := map[string][]byte{}
	add := func(path, asset string, payload []byte) {
		digest := sha256.Sum256(payload)
		manifest.Artifacts = append(manifest.Artifacts, bundleArtifact{Path: path, Asset: asset, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))})
		files[asset] = payload
	}
	library := "runtime/libonnxruntime.so"
	if runtime.GOOS == "darwin" {
		library = "runtime/libonnxruntime.dylib"
	}
	add(library, "ort.bin", []byte("ort-runtime"))
	for _, variant := range []string{"typed-decisions", "multilingual"} {
		prefix := "checkpoints/" + variant + "/"
		add(prefix+"laya.onnx", variant+"-model.onnx", []byte(variant+"-onnx"))
		add(prefix+"rl_agent_config.json", variant+"-config.json", []byte(`{"max_len":512,"head_max_len":192}`))
		add(prefix+"tokenizer/tokenizer.json", variant+"-tokenizer.json", []byte(`{"version":"1.0"}`))
		add(prefix+"tokenizer/tokenizer_config.json", variant+"-tokenizer-config.json", []byte(`{"mask_token":"[MASK]"}`))
	}
	return manifest, files
}
