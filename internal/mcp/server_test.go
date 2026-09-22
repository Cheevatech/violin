package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestToolsExposeEmbeddedLayaOperations(t *testing.T) {
	value := tools()["tools"].([]map[string]any)
	want := map[string]bool{"laya_route": false, "laya_review_risk": false, "laya_check_job": false, "laya_wait_job": false, "laya_explain_decision": false}
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
