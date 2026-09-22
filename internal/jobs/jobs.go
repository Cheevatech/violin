package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/film/violin/internal/auth"
	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
	"github.com/film/violin/internal/laya"
	"github.com/film/violin/internal/models"
)

type Options struct {
	Root, Workspace, Backend, RequestedBackend, Mode, Task string
	Timeout, IdleTimeout                                   int
	TimeoutSource                                          string
}
type Descriptor struct {
	AgentID          string    `json:"agent_id"`
	Backend          string    `json:"backend"`
	RequestedBackend string    `json:"requested_backend"`
	PID              int       `json:"pid"`
	Lease            string    `json:"lease"`
	Evidence         string    `json:"evidence"`
	Output           string    `json:"output"`
	Status           string    `json:"status"`
	TaskFile         string    `json:"task_file"`
	Mode             string    `json:"mode"`
	Timeout          int       `json:"effective_timeout_seconds"`
	TimeoutSource    string    `json:"timeout_source"`
	IdleTimeout      int       `json:"idle_timeout_seconds"`
	IdleEnabled      bool      `json:"idle_timeout_enabled"`
	CreatedAt        time.Time `json:"created_at"`
	LayaMode         string    `json:"laya_mode"`
	LayaFallback     bool      `json:"laya_fallback"`
	LayaModelVersion string    `json:"laya_model_version,omitempty"`
	LayaError        string    `json:"laya_error,omitempty"`
	OwnerPID         int       `json:"owner_pid"`
}
type Job struct {
	Descriptor Descriptor
	path       string
	cmd        *exec.Cmd
}

type AuthRequiredError struct {
	Backend   string
	Transport string
	Action    string
	Message   string
}

func (e AuthRequiredError) Error() string { return "auth_required: " + e.Message }
func (e AuthRequiredError) Details() map[string]any {
	return map[string]any{"status": "auth_required", "backend": e.Backend, "transport": e.Transport, "scope": "user", "action": e.Action, "message": e.Message}
}

func Spawn(o Options) (*Job, error) {
	cfg, err := config.LoadFor(o.Workspace)
	if err != nil {
		return nil, err
	}
	if o.Backend == "" {
		o.Backend = "auto"
	}
	if o.RequestedBackend == "" {
		o.RequestedBackend = o.Backend
	}
	if o.Mode == "" {
		o.Mode = "inspect"
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = 300
	}
	if !filepath.IsAbs(o.Workspace) {
		return nil, errors.New("workspace must be absolute")
	}
	if _, err := os.Stat(o.Workspace); err != nil {
		return nil, err
	}
	decision := evaluateDecision(o.Root, o.Task, o.Backend)
	if o.Backend == "auto" && os.Getenv("VIOLIN_LAYA_MODE") == "active" && !decision.Fallback && len(decision.Answers) > 0 {
		if selected, ok := decision.Answers[0].Value.(string); ok && (selected == "agy" || selected == "qwen" || selected == "claude") {
			o.Backend = selected
		}
	}
	if o.Backend == "auto" {
		order := cfg.Scheduler.Order
		if len(order) == 0 {
			order = []string{"agy", "qwen", "claude"}
		}
		readyOrder := make([]string, 0, len(order))
		var authError error
		for _, candidate := range order {
			candidateError := checkAuth(candidate, cfg.Backend[candidate])
			if candidateError != nil {
				if authError == nil {
					authError = candidateError
				}
				continue
			}
			readyOrder = append(readyOrder, candidate)
		}
		if len(readyOrder) == 0 && authError != nil {
			return nil, authError
		}
		o.Backend, err = selectBackend(o.Root, readyOrder, cfg.Backend)
		if err != nil {
			return nil, err
		}
	}
	if o.Backend != "agy" && o.Backend != "qwen" && o.Backend != "claude" {
		return nil, errors.New("invalid backend")
	}
	if authError := checkAuth(o.Backend, cfg.Backend[o.Backend]); authError != nil {
		return nil, authError
	}
	if o.Timeout <= 0 {
		o.Timeout = cfg.Timeouts.Defaults[o.Mode]
	}
	if o.Timeout < 1 || o.Timeout > cfg.Timeouts.MaxSeconds {
		return nil, fmt.Errorf("timeout_seconds must be between 1 and %d", cfg.Timeouts.MaxSeconds)
	}
	if o.TimeoutSource == "" {
		o.TimeoutSource = "default:" + o.Mode
	}
	if err := os.MkdirAll(o.Root, 0700); err != nil {
		return nil, err
	}
	run, err := os.MkdirTemp(o.Root, o.Backend+"-")
	if err != nil {
		return nil, err
	}
	taskPath := filepath.Join(run, "task.txt")
	if err = os.WriteFile(taskPath, []byte(o.Task), 0600); err != nil {
		return nil, err
	}
	outputPath := filepath.Join(run, "output.json")
	statusPath := filepath.Join(run, "status.json")
	worker := os.Getenv("VIOLIN_WORKER_BIN")
	native := cfg.Backend[o.Backend].Transport != "" || cfg.Backend[o.Backend].Command != nil || len(cfg.Backend[o.Backend].CLI.Command) > 0
	workerArgs := []string{o.Backend, "--mode", o.Mode, "-C", o.Workspace, "--task-file", taskPath, "--timeout", strconv.Itoa(o.Timeout), "--idle-timeout", strconv.Itoa(o.IdleTimeout)}
	if native {
		worker = os.Args[0]
		workerArgs = append([]string{"worker"}, workerArgs...)
	} else if worker == "" {
		worker = filepath.Join(filepath.Dir(os.Args[0]), "..", "bin", "violin-worker")
	}
	if _, err = os.Stat(worker); err != nil {
		worker = filepath.Join(filepath.Dir(os.Args[0]), "violin-worker")
	}
	cmd := exec.Command(worker, workerArgs...)
	stdout, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	stderr, err := os.OpenFile(filepath.Join(run, "stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr, cmd.Dir = stdout, stderr, o.Workspace
	cmd.Env = workerEnv(cfg, o.Backend, statusPath, o.Root, o.TimeoutSource)
	cmd.Env = append(cmd.Env, "VIOLIN_WORKER_EVIDENCE="+run)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	_ = stdout.Close()
	_ = stderr.Close()
	d := Descriptor{AgentID: fmt.Sprintf("%d-%d", time.Now().UnixNano(), cmd.Process.Pid), Backend: o.Backend, RequestedBackend: o.RequestedBackend, PID: cmd.Process.Pid, Evidence: run, Output: outputPath, Status: statusPath, TaskFile: taskPath, Mode: o.Mode, Timeout: o.Timeout, TimeoutSource: o.TimeoutSource, IdleTimeout: o.IdleTimeout, IdleEnabled: !(o.Backend == "agy" || o.Backend == "qwen"), OwnerPID: os.Getpid(), CreatedAt: time.Now(), LayaMode: os.Getenv("VIOLIN_LAYA_MODE"), LayaFallback: decision.Fallback, LayaModelVersion: decision.ModelVersion, LayaError: decision.Error}
	if d.LayaMode == "" {
		d.LayaMode = "shadow"
	}
	j := &Job{Descriptor: d, path: filepath.Join(o.Root, "jobs", d.AgentID+".json"), cmd: cmd}
	if err = os.MkdirAll(filepath.Dir(j.path), 0700); err != nil {
		return nil, err
	}
	if err = j.save(); err != nil {
		return nil, err
	}
	return j, nil
}

func checkAuth(provider string, backend config.Backend) error {
	// Configured legacy/custom workers own their authentication contract.
	if backend.Command != nil || backend.Transport == "" {
		return nil
	}
	status, err := auth.NewManager(credentials.Default()).StatusWithBackend(context.Background(), provider, backend)
	if err != nil {
		return err
	}
	if status.Authenticated {
		return nil
	}
	return AuthRequiredError{Backend: provider, Transport: backend.Transport, Action: status.Action, Message: status.Message}
}

func Open(root, id string) (*Job, error) {
	path := filepath.Join(root, "jobs", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Descriptor
	if err = json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &Job{Descriptor: d, path: path}, nil
}
func (j *Job) save() error {
	data, err := json.MarshalIndent(j.Descriptor, "", "  ")
	if err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, j.path)
}
func (j *Job) alive() bool {
	if data, err := os.ReadFile(j.Descriptor.Output); err == nil {
		var report map[string]any
		if json.Unmarshal(data, &report) == nil && report["status"] != nil {
			return false
		}
	}
	if j.cmd != nil && j.cmd.ProcessState == nil {
		return true
	}
	p, err := os.FindProcess(j.Descriptor.PID)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
func (j *Job) Live() map[string]any {
	return map[string]any{"agent_id": j.Descriptor.AgentID, "status": "running", "selected_backend": j.Descriptor.Backend, "requested_backend": j.Descriptor.RequestedBackend, "evidence": j.Descriptor.Evidence, "effective_timeout_seconds": j.Descriptor.Timeout, "timeout_source": j.Descriptor.TimeoutSource, "idle_timeout_seconds": j.Descriptor.IdleTimeout, "idle_timeout_enabled": j.Descriptor.IdleEnabled, "laya_mode": j.Descriptor.LayaMode, "laya_fallback": j.Descriptor.LayaFallback, "laya_model_version": j.Descriptor.LayaModelVersion}
}

func (j *Job) Wait(seconds int) (any, error) {
	if j.cmd != nil {
		if seconds <= 0 {
			_ = j.cmd.Wait()
			j.cmd = nil
		} else {
			done := make(chan error, 1)
			go func() { done <- j.cmd.Wait() }()
			select {
			case <-done:
				j.cmd = nil
			case <-time.After(time.Duration(seconds) * time.Second):
				return j.Live(), nil
			}
		}
	} else if seconds > 0 {
		deadline := time.Now().Add(time.Duration(seconds) * time.Second)
		for j.alive() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if j.alive() {
		return j.Live(), nil
	}
	return j.finish()
}
func (j *Job) finish() (map[string]any, error) {
	data, err := os.ReadFile(j.Descriptor.Output)
	var value map[string]any
	if err == nil {
		_ = json.Unmarshal(data, &value)
	}
	if value == nil {
		value = map[string]any{"status": "failed", "error_message": "worker ended without a valid report", "evidence": j.Descriptor.Evidence}
	}
	value["agent_id"] = j.Descriptor.AgentID
	value["selected_backend"] = j.Descriptor.Backend
	value["requested_backend"] = j.Descriptor.RequestedBackend
	_ = os.Remove(j.path)
	return value, nil
}
func (j *Job) Interrupt() (map[string]any, error) {
	_ = syscall.Kill(-j.Descriptor.PID, syscall.SIGINT)
	if j.cmd != nil {
		done := make(chan struct{})
		go func() { _, _ = j.cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
		}
	} else {
		deadline := time.Now().Add(8 * time.Second)
		for j.alive() && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
	}
	value, _ := j.finish()
	return value, nil
}
func List(root string) ([]map[string]any, error) {
	directory := filepath.Join(root, "jobs")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		j, err := Open(root, id)
		if err != nil {
			continue
		}
		if j.alive() {
			result = append(result, j.Live())
		} else if value, err := j.finish(); err == nil {
			result = append(result, value)
		}
	}
	return result, nil
}

// InterruptOwned stops only jobs spawned by this server process. Descriptors
// from an earlier server remain recoverable after restart.
func InterruptOwned(root string, ownerPID int) {
	entries, err := os.ReadDir(filepath.Join(root, "jobs"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := Open(root, strings.TrimSuffix(entry.Name(), ".json"))
		if err == nil && job.Descriptor.OwnerPID == ownerPID && job.alive() {
			_, _ = job.Interrupt()
		}
	}
}

func selectBackend(root string, order []string, backendConfig map[string]config.Backend) (string, error) {
	if len(order) == 0 {
		order = []string{"agy", "qwen", "claude"}
	}
	counts := map[string]int{}
	entries, err := os.ReadDir(filepath.Join(root, "jobs"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := Open(root, strings.TrimSuffix(entry.Name(), ".json"))
		if err == nil && job.alive() {
			counts[job.Descriptor.Backend]++
		}
	}
	index := 0
	statePath := filepath.Join(root, "round-robin.json")
	if data, err := os.ReadFile(statePath); err == nil {
		var state struct {
			Index int `json:"index"`
		}
		if json.Unmarshal(data, &state) == nil {
			index = state.Index % len(order)
		}
	}
	for offset := 0; offset < len(order); offset++ {
		candidate := order[(index+offset)%len(order)]
		limit := backendConfig[candidate].MaxConcurrency
		if limit == 0 {
			limit = 1
		}
		if counts[candidate] < limit {
			next := (index + offset + 1) % len(order)
			data, _ := json.Marshal(map[string]int{"index": next})
			_ = os.WriteFile(statePath, data, 0600)
			return candidate, nil
		}
	}
	return "", errors.New("worker capacity is full")
}

func workerEnv(cfg config.Config, backend, statusPath, root, timeoutSource string) []string {
	env := os.Environ()
	env = append(env, "VIOLIN_WORKER_STATUS="+statusPath, "VIOLIN_WORKER_RUNS="+root, "VIOLIN_TIMEOUT_SOURCE="+timeoutSource)
	item, ok := cfg.Backend[backend]
	if !ok || item.Command == nil {
		return env
	}
	data, _ := json.Marshal(item.Command)
	prefix := "VIOLIN_" + strings.ToUpper(backend) + "_"
	env = append(env, prefix+"COMMAND="+string(data), prefix+"CUSTOM=1", prefix+"PROTOCOL="+item.Protocol, prefix+"STDIN="+strconv.FormatBool(item.Stdin))
	return env
}

func evaluateDecision(root, task, requested string) laya.Result {
	manager, err := models.NewManager(filepath.Join(root, "models"))
	if err != nil {
		return laya.Result{Fallback: true, Error: err.Error()}
	}
	engine := laya.ManagedEngine{Manager: manager, Fallback: laya.FallbackEngine{}}
	result, _ := engine.Evaluate(laya.Request{Language: laya.ProtocolLanguage, State: map[string]any{"task": task}, Questions: []laya.Question{{ID: "backend", Kind: laya.Choice, Options: []string{"agy", "qwen", "claude"}, Fallback: requested}}})
	return result
}
