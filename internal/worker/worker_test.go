package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseCLIOutputSupportsCommonJSONResultFields(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{`{"response":"response text"}`, "response text"},
		{`{"result":"result text"}`, "result text"},
		{"plain text", "plain text"},
	} {
		if got := parseCLIOutput([]byte(test.input)); got != test.want {
			t.Fatalf("input=%q got=%q want=%q", test.input, got, test.want)
		}
	}
}

func TestParseProviderOutputUnderstandsProviderEventStreams(t *testing.T) {
	qwen, err := parseProviderOutput([]byte("{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"reasoned \"}}\n{\"type\":\"turn.completed\"}\n"), "qwen")
	if err != nil || qwen != "reasoned " {
		t.Fatalf("qwen=%q err=%v", qwen, err)
	}
	claude, err := parseProviderOutput([]byte(`{"type":"result","subtype":"success","result":"claude final"}`), "claude")
	if err != nil || claude != "claude final" {
		t.Fatalf("claude=%q err=%v", claude, err)
	}
	if _, err := parseProviderOutput([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"expired"}`), "claude"); err == nil {
		t.Fatal("expected provider event error")
	}
}

func TestParseWorkerArgsIncludesIdleTimeout(t *testing.T) {
	options, err := Parse([]string{"qwen", "--task-file", "/tmp/task", "--idle-timeout", "42"})
	if err != nil {
		t.Fatal(err)
	}
	if options.IdleTimeout != 42 {
		t.Fatalf("options=%+v", options)
	}
}

func TestCommandPartsAcceptsLegacyConfiguredCommand(t *testing.T) {
	if got := commandParts([]any{"fake-agent", "--json"}); len(got) != 2 || got[0] != "fake-agent" {
		t.Fatalf("got=%v", got)
	}
}

func TestRunAPITransportWritesResponsesReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request: %s %s", request.URL.Path, request.Header.Get("Authorization"))
		}
		_, _ = writer.Write([]byte(`{"output":[{"content":[{"text":"native go response"}]}]}`))
	}))
	defer server.Close()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	config := "[backend.qwen]\ntransport = \"api\"\n[backend.qwen.api]\nbase_url = \"" + server.URL + "\"\nmodel = \"test-model\"\nwire_api = \"responses\"\napi_key_env = \"VIOLIN_TEST_KEY\"\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_CONFIG", configPath)
	t.Setenv("VIOLIN_TEST_KEY", "test-key")
	evidence := filepath.Join(root, "evidence")
	if err := os.Mkdir(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIOLIN_WORKER_EVIDENCE", evidence)
	t.Setenv("VIOLIN_TIMEOUT_SOURCE", "test")
	task := filepath.Join(root, "task.txt")
	if err := os.WriteFile(task, []byte("say hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), Options{Backend: "qwen", Mode: "inspect", Workspace: workspace, TaskFile: task, Timeout: 10, IdleTimeout: 2}); err != nil {
		t.Fatal(err)
	}
	reportData, err := os.ReadFile(filepath.Join(evidence, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatal(err)
	}
	if report["status"] != "completed" || report["summary"] != "native go response" || report["idle_timeout_enabled"] != false {
		t.Fatalf("unexpected report: %+v", report)
	}
	if changed, ok := report["changed_files"].([]any); !ok || changed == nil {
		t.Fatalf("changed_files must be an array: %#v", report["changed_files"])
	}
}

func TestRunCLIReportsIdleTimeoutAfterProviderEvent(t *testing.T) {
	options := Options{Backend: "claude", Workspace: t.TempDir(), Timeout: 5, IdleTimeout: 1, IdleTimeoutEnabled: true}
	_, err := runCLI(context.Background(), []string{"/bin/sh", "-c", "printf '%s\\n' '\"type\":\"item.completed\"'; sleep 2"}, "task", options)
	if err == nil {
		t.Fatal("expected idle timeout")
	}
	var execution executionError
	if !errors.As(err, &execution) || execution.status != "idle_timeout" {
		t.Fatalf("err=%v", err)
	}
}
