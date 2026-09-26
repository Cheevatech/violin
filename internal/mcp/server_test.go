package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/film/violin/internal/laya"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolsExposeEmbeddedLayaOperations(t *testing.T) {
	value := tools()["tools"].([]map[string]any)
	want := map[string]bool{"laya_route": false, "laya_review_risk": false, "laya_check_job": false, "laya_wait_job": false, "laya_explain_decision": false, "laya_feedback": false}
	for _, item := range value {
		if name, ok := item["name"].(string); ok {
			if _, exists := want[name]; exists {
				want[name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing embedded Laya tool %q", name)
		}
	}
}

func TestActiveSpawnAbstainsOnMissingModelAndDoesNotCreateWorker(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("[laya]\nmode = \"active\"\ntimeout_seconds = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)
	t.Setenv("VIOLIN_WORKER_RUNS", filepath.Join(root, "runs"))
	result, err := call(map[string]any{"name": "spawn_agent", "arguments": map[string]any{"task": "inspect this repository", "cwd": root, "mode": "auto"}})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := result.(map[string]any)
	if !ok || value["status"] != "review_required" || value["reason"] != "laya_fallback_or_missing_decision" {
		t.Fatalf("unexpected spawn result: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "jobs")); !os.IsNotExist(err) {
		t.Fatalf("abstain spawned worker: %v", err)
	}
}

func TestLayaFeedbackStoresCorrectedLabelsWithoutTaskText(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VIOLIN_WORKER_RUNS", root)
	if err := laya.AppendFeedback(root, laya.FeedbackEvent{AgentID: "123-456", SelectedBackend: "agy", Outcome: "completed"}); err != nil {
		t.Fatal(err)
	}
	result, err := call(map[string]any{"name": "laya_feedback", "arguments": map[string]any{"agent_id": "123-456", "backend": "qwen", "mode": "inspect", "risk": "low", "timeout_policy": "standard", "retry_policy": "never", "outcome": "completed"}})
	if err != nil || result.(map[string]any)["status"] != "recorded" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "laya-feedback.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"corrected_backend":"qwen"`)) || !bytes.Contains(data, []byte(`"corrected_timeout_policy":"standard"`)) || bytes.Contains(data, []byte(`"task"`)) {
		t.Fatalf("unsafe/incomplete feedback: %s", data)
	}
}

func TestSpawnIntegerArgumentsRejectFractionsAndStrings(t *testing.T) {
	for _, value := range []any{float64(1.5), "30", float64(-1)} {
		if _, _, err := intArg(map[string]any{"timeout_seconds": value}, "timeout_seconds"); err == nil {
			t.Fatalf("accepted invalid integer %v", value)
		}
	}
}

func TestLayaRouteUsesFallbackWithoutSpawningWorker(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("[laya]\nmode = \"shadow\"\ntimeout_seconds = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)
	t.Setenv("VIOLIN_WORKER_RUNS", filepath.Join(root, "runs"))
	result, err := call(map[string]any{"name": "laya_route", "arguments": map[string]any{"task": "inspect the repository"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if value["fallback"] != true || value["decision"] == nil {
		t.Fatalf("unexpected route=%s", data)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "jobs")); !os.IsNotExist(err) {
		t.Fatalf("route must not create worker jobs: %v", err)
	}
}

func TestLayaReviewRiskIsReadOnly(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("[laya]\nmode = \"shadow\"\ntimeout_seconds = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)
	t.Setenv("VIOLIN_WORKER_RUNS", filepath.Join(root, "runs"))
	result, err := call(map[string]any{"name": "laya_review_risk", "arguments": map[string]any{"task": "implement a security fix"}})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := result.(map[string]any)
	if !ok || value["risk"] == nil || value["decision"] == nil {
		t.Fatalf("unexpected risk result=%#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "jobs")); !os.IsNotExist(err) {
		t.Fatalf("risk review must not create worker jobs: %v", err)
	}
}

func TestRunServesLayaToolsThroughMCPProtocol(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}
`)
	var output bytes.Buffer
	if err := Run(input, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"laya_route"`)) {
		t.Fatalf("MCP tools/list missing Laya route: %s", output.String())
	}
}

func TestRealUpstreamLayaThroughMCP(t *testing.T) {
	assets := os.Getenv("VIOLIN_LAYA_TEST_BUNDLE_ASSETS")
	if assets == "" {
		t.Skip("set VIOLIN_LAYA_TEST_BUNDLE_ASSETS to a packaged ONNX bundle")
	}
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("[laya]\nmode = \"advisory\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)
	t.Setenv("VIOLIN_WORKER_RUNS", filepath.Join(root, "runs"))
	server := httptest.NewServer(http.FileServer(http.Dir(assets)))
	defer server.Close()
	t.Setenv("VIOLIN_LAYA_BUNDLE_BASE_URL", server.URL)
	if err := laya.EnsureUpstream(context.Background(), filepath.Join(root, "runs")); err != nil {
		t.Fatalf("install bundle: %v", err)
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"laya_route","arguments":{"task":"Please inspect this Go change","mode":"inspect","backend":"auto"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"laya_route","arguments":{"task":"ช่วยตรวจการเปลี่ยนแปลง Go นี้","mode":"inspect","backend":"auto"}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := Run(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for _, want := range []string{"typed-decisions", "multilingual"} {
		var envelope response
		if err := decoder.Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		result, ok := envelope.Result.(map[string]any)
		if !ok {
			t.Fatalf("MCP result=%#v", envelope.Result)
		}
		content := result["content"].([]any)[0].(map[string]any)
		var route laya.Result
		if err := json.Unmarshal([]byte(content["text"].(string)), &route); err != nil {
			t.Fatal(err)
		}
		if route.Fallback || route.Decision == nil || !strings.Contains(route.ModelVersion, "/"+want+"@") {
			t.Fatalf("MCP route expected real %s result, got %+v", want, route)
		}
	}
}

func TestWaitArgumentContract(t *testing.T) {
	for _, item := range []struct {
		value any
		want  int
		bad   bool
	}{
		{nil, 50, false}, {float64(0), 0, false}, {float64(50), 50, false}, {float64(300), 50, false}, {float64(1.5), 0, true}, {"2", 0, true}, {float64(-1), 0, true},
	} {
		args := map[string]any{}
		if item.value != nil {
			args["wait_seconds"] = item.value
		}
		got, err := waitArg(args)
		if (err != nil) != item.bad || (!item.bad && got != item.want) {
			t.Fatalf("value=%v got=%d err=%v", item.value, got, err)
		}
	}
	for _, tool := range tools()["tools"].([]map[string]any) {
		if tool["name"] == "laya_wait_job" || tool["name"] == "wait_agent" {
			schema := tool["inputSchema"].(map[string]any)
			props := schema["properties"].(map[string]any)
			if props["wait_seconds"].(map[string]any)["maximum"] != 50 {
				t.Fatal("incorrect wait schema")
			}
		}
	}
}
