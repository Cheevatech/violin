package laya

import (
	"strings"
	"testing"
	"time"
)

func TestUpstreamEngineJSONLinesPreservesUnicode(t *testing.T) {
	engine, err := StartUpstream(t.Context(), t.TempDir(), []string{"/bin/cat"})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	request := Request{Language: "th", State: map[string]any{"task": "ช่วยตรวจโค้ดนี้หน่อย"}, Questions: []Question{{ID: "backend", Kind: Choice, Options: []string{"agy", "qwen"}}}}
	result, err := engine.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelVersion != "laya-"+UpstreamVersion+"@"+CheckpointRevision {
		t.Fatalf("version=%q", result.ModelVersion)
	}
	if result.Fallback {
		t.Fatal("runner echo should not be marked as fallback")
	}
}

func TestUpstreamEngineReportsMalformedAndExitedRunner(t *testing.T) {
	for _, tc := range []struct{ name, command, want string }{
		{"malformed", "printf 'not-json\\n'", "decode Laya runner response"},
		{"exit", "exit 9", "read Laya runner response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := StartUpstream(t.Context(), t.TempDir(), []string{"/bin/sh", "-c", tc.command})
			if err != nil {
				t.Fatal(err)
			}
			_, err = engine.Evaluate(Request{State: map[string]any{"task": "x"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want substring %q", err, tc.want)
			}
			_ = engine.Close()
		})
	}
}

func TestUpstreamEngineTimeout(t *testing.T) {
	engine, err := StartUpstream(t.Context(), t.TempDir(), []string{"/bin/sh", "-c", "sleep 10"})
	if err != nil {
		t.Fatal(err)
	}
	engine.timeout = 20 * time.Millisecond
	_, err = engine.Evaluate(Request{State: map[string]any{"task": "test"}})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err=%v", err)
	}
	_ = engine.Close()
}
