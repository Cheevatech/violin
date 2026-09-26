package laya

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed runner.py pyproject.toml uv.lock
var upstreamRunner embed.FS

const (
	UpstreamVersion    = "0.3.20"
	CheckpointRevision = "55cf4c4ebb4ebe31b2550e8bdf3bd21b99753851"
)

type UpstreamStatus struct {
	Installed          bool   `json:"installed"`
	FallbackInUse      bool   `json:"fallback_in_use"`
	SDKVersion         string `json:"sdk_version"`
	CheckpointRevision string `json:"checkpoint_revision"`
	Device             string `json:"device,omitempty"`
	Runtime            string `json:"runtime,omitempty"`
	Error              string `json:"error,omitempty"`
}

// UpstreamEngine owns one persistent Python SDK runner for an MCP session.
type UpstreamEngine struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *bufio.Reader
	mu      sync.Mutex
	version string
	timeout time.Duration
	closed  bool
}

func EnsureUpstream(ctx context.Context, root string) error {
	base := filepath.Join(root, "laya")
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	runner, err := upstreamRunner.ReadFile("runner.py")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(base, "runner.py"), runner, 0600); err != nil {
		return err
	}
	for _, name := range []string{"pyproject.toml", "uv.lock"} {
		data, readErr := upstreamRunner.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		if writeErr := os.WriteFile(filepath.Join(base, name), data, 0600); writeErr != nil {
			return writeErr
		}
	}
	uv, lookupErr := exec.LookPath("uv")
	if lookupErr != nil {
		return errors.New("uv is required to provision the locked Laya runtime; install uv and rerun violin install")
	}
	python := filepath.Join(root, "laya", "venv", "bin", "python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(root, "laya", "venv", "Scripts", "python.exe")
	}
	if _, err := os.Stat(python); err != nil {
		pythonVersion := "3.12"
		installPython := exec.CommandContext(ctx, uv, "python", "install", pythonVersion)
		installPython.Stdout, installPython.Stderr = os.Stdout, os.Stderr
		if err := installPython.Run(); err != nil {
			return fmt.Errorf("install managed CPython 3.12: %w", err)
		}
		venv := filepath.Join(base, "venv")
		create := exec.CommandContext(ctx, uv, "venv", "--python", pythonVersion, venv)
		create.Stdout, create.Stderr = os.Stdout, os.Stderr
		if err := create.Run(); err != nil {
			return fmt.Errorf("create managed Python environment: %w", err)
		}
	}
	venv := filepath.Join(base, "venv")
	install := exec.CommandContext(ctx, uv, "sync", "--project", base, "--locked", "--no-install-project")
	install.Env = append(os.Environ(), "UV_PROJECT_ENVIRONMENT="+venv)
	install.Stdout, install.Stderr = os.Stdout, os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("install locked upstream Laya SDK dependencies: %w", err)
	}
	cmd := exec.CommandContext(ctx, python, "-c", "import importlib.metadata, torch; print(importlib.metadata.version('laya')); print(torch.backends.mps.is_available() if hasattr(torch.backends, 'mps') else False)")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("verify managed Laya SDK: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 || lines[0] != UpstreamVersion {
		return fmt.Errorf("managed Laya SDK version mismatch: %q", strings.TrimSpace(string(out)))
	}
	// Remove only the legacy local Qwen weights/cache payload after the pinned
	// upstream SDK has passed its import/version check. llama.cpp and provider
	// configuration are separate and are deliberately untouched.
	_ = os.RemoveAll(filepath.Join(base, "models", "qwen3-4b-q4km-2f3b082"))
	_ = os.RemoveAll(filepath.Join(base, "hf-cache", "hub", "models--ggml-org--Qwen3-4B-GGUF"))
	return nil
}

func UpstreamRuntimeStatus(root string) UpstreamStatus {
	s := UpstreamStatus{SDKVersion: UpstreamVersion, CheckpointRevision: CheckpointRevision, Device: "cpu", FallbackInUse: true}
	python := filepath.Join(root, "laya", "venv", "bin", "python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(root, "laya", "venv", "Scripts", "python.exe")
	}
	if _, err := os.Stat(python); err != nil {
		s.Error = "managed Python runtime is not installed"
		return s
	}
	s.Runtime = python
	cmd := exec.Command(python, "-c", "import importlib.metadata, torch; print(importlib.metadata.version('laya')); print('mps' if hasattr(torch.backends, 'mps') and torch.backends.mps.is_available() else ('cuda' if torch.cuda.is_available() else 'cpu'))")
	out, err := cmd.Output()
	if err != nil {
		s.Error = err.Error()
		return s
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || lines[0] != UpstreamVersion {
		s.Error = "managed Laya SDK version mismatch"
		return s
	}
	s.Device, s.Installed, s.FallbackInUse = lines[1], true, false
	return s
}

func StartUpstream(ctx context.Context, root string, argv []string) (*UpstreamEngine, error) {
	if len(argv) == 0 {
		python := filepath.Join(root, "laya", "venv", "bin", "python")
		if runtime.GOOS == "windows" {
			python = filepath.Join(root, "laya", "venv", "Scripts", "python.exe")
		}
		argv = []string{python, filepath.Join(root, "laya", "runner.py")}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "VIOLIN_LAYA_CHECKPOINT_REVISION="+CheckpointRevision,
		"HF_HOME="+filepath.Join(root, "laya", "upstream-hf-cache"))
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &UpstreamEngine{cmd: cmd, in: in, out: bufio.NewReader(out), version: "laya-" + UpstreamVersion + "@" + CheckpointRevision, timeout: 5 * time.Minute}, nil
}

func (e *UpstreamEngine) Evaluate(request Request) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return Result{}, errors.New("Laya runner is closed")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	if _, err = e.in.Write(append(data, '\n')); err != nil {
		return Result{}, err
	}
	type readResult struct {
		line []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() { line, readErr := e.out.ReadBytes('\n'); read <- readResult{line, readErr} }()
	var line []byte
	select {
	case response := <-read:
		line, err = response.line, response.err
		if err != nil {
			return Result{}, fmt.Errorf("read Laya runner response: %w", err)
		}
	case <-time.After(e.timeout):
		_ = e.cmd.Process.Kill()
		return Result{}, errors.New("upstream Laya inference timed out")
	}
	var result Result
	if err := json.Unmarshal(line, &result); err != nil {
		return Result{}, fmt.Errorf("decode Laya runner response: %w", err)
	}
	result.ModelVersion = e.version
	if result.Decision != nil {
		result.Decision.ModelVersion = e.version
	}
	return result, nil
}

func (e *UpstreamEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	_ = e.in.Close()
	done := make(chan error, 1)
	go func() { done <- e.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		_ = e.cmd.Process.Kill()
		return <-done
	}
}
