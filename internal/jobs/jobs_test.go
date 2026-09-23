package jobs

import (
	"encoding/json"
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

func TestInterruptDoesNotSignalUnverifiedPID(t *testing.T) {
	j := &Job{Descriptor: Descriptor{PID: os.Getpid(), TaskFile: "/impossible/task.txt"}}
	if _, err := j.Interrupt(); err == nil {
		t.Fatal("accepted unrelated PID")
	}
}
