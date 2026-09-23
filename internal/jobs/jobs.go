package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/film/violin/internal/auth"
	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
	"github.com/film/violin/internal/laya"
	"github.com/film/violin/internal/models"
	"github.com/film/violin/internal/supervisor"
)

type Options struct {
	Root, Workspace, Backend, RequestedBackend, Mode, Task string
	OwnerInstance, SessionID                               string
	Timeout, IdleTimeout                                   int
	TimeoutSource                                          string
}
type Descriptor struct {
	AgentID          string         `json:"agent_id"`
	Backend          string         `json:"backend"`
	RequestedBackend string         `json:"requested_backend"`
	PID              int            `json:"pid"`
	Lease            string         `json:"lease"`
	Evidence         string         `json:"evidence"`
	Output           string         `json:"output"`
	Status           string         `json:"status"`
	TaskFile         string         `json:"task_file"`
	Mode             string         `json:"mode"`
	Timeout          int            `json:"effective_timeout_seconds"`
	TimeoutSource    string         `json:"timeout_source"`
	IdleTimeout      int            `json:"idle_timeout_seconds"`
	IdleEnabled      bool           `json:"idle_timeout_enabled"`
	CreatedAt        time.Time      `json:"created_at"`
	LayaMode         string         `json:"laya_mode"`
	LayaFallback     bool           `json:"laya_fallback"`
	LayaModelVersion string         `json:"laya_model_version,omitempty"`
	LayaError        string         `json:"laya_error,omitempty"`
	LayaDecision     *laya.Decision `json:"laya_decision,omitempty"`
	OwnerPID         int            `json:"owner_pid"`
	OwnerInstance    string         `json:"owner_instance,omitempty"`
	SessionID        string         `json:"session_id,omitempty"`
	SupervisorMode   string         `json:"supervisor_mode"`
	SupervisorState  string         `json:"supervisor_state"`
	SupervisorStale  int            `json:"supervisor_stale_seconds"`
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
	layaMode := cfg.Laya.Mode
	if layaMode == "" {
		layaMode = "shadow"
	}
	decision := EvaluateLaya(o.Root, o.Task, o.Mode, o.Backend, cfg)
	if err := os.MkdirAll(o.Root, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(o.Root, "scheduler.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	roundRobinPath := filepath.Join(o.Root, "round-robin.json")
	oldRoundRobin, roundRobinErr := os.ReadFile(roundRobinPath)
	spawned := false
	defer func() {
		if spawned {
			return
		}
		if roundRobinErr == nil {
			_ = os.WriteFile(roundRobinPath, oldRoundRobin, 0600)
		} else if os.IsNotExist(roundRobinErr) {
			_ = os.Remove(roundRobinPath)
		}
	}()
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
		preferred := []string(nil)
		if layaMode == "active" && !decision.Fallback && decision.Decision != nil && decision.Decision.Confidence >= 0.80 && decision.Decision.Margin >= 0.15 {
			preferred = decision.Decision.BackendCandidates
		}
		o.Backend, err = selectBackend(o.Root, readyOrder, cfg.Backend, preferred, cfg.Scheduler, sessionID(o), true)
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
	if o.RequestedBackend != "auto" {
		if _, err := selectBackend(o.Root, []string{o.Backend}, cfg.Backend, nil, cfg.Scheduler, sessionID(o), false); err != nil {
			return nil, err
		}
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
	d := Descriptor{AgentID: fmt.Sprintf("%d-%d", time.Now().UnixNano(), cmd.Process.Pid), Backend: o.Backend, RequestedBackend: o.RequestedBackend, PID: cmd.Process.Pid, Lease: fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid()), Evidence: run, Output: outputPath, Status: statusPath, TaskFile: taskPath, Mode: o.Mode, Timeout: o.Timeout, TimeoutSource: o.TimeoutSource, IdleTimeout: o.IdleTimeout, IdleEnabled: config.IdleTimeoutEnabled(o.Backend, cfg.Backend[o.Backend]), OwnerPID: os.Getpid(), OwnerInstance: o.OwnerInstance, SessionID: sessionID(o), CreatedAt: time.Now(), LayaMode: layaMode, LayaFallback: decision.Fallback, LayaModelVersion: decision.ModelVersion, LayaError: decision.Error, LayaDecision: decision.Decision, SupervisorMode: cfg.Laya.Supervisor.Mode, SupervisorState: "starting", SupervisorStale: cfg.Laya.Supervisor.StaleSeconds}
	j := &Job{Descriptor: d, path: filepath.Join(o.Root, "jobs", d.AgentID+".json"), cmd: cmd}
	if err = os.MkdirAll(filepath.Dir(j.path), 0700); err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil, err
	}
	if err = j.save(); err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil, err
	}
	spawned = true
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
	if !validID.MatchString(id) {
		return nil, errors.New("invalid agent id")
	}
	path := filepath.Join(root, "jobs", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Descriptor
	if err = json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	if d.AgentID != id || d.PID <= 0 || !strings.HasSuffix(id, "-"+strconv.Itoa(d.PID)) {
		return nil, errors.New("invalid job descriptor")
	}
	run, err := filepath.Abs(d.Evidence)
	if err != nil {
		return nil, err
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	canonicalBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, err
	}
	canonicalRun, err := filepath.EvalSymlinks(run)
	if err != nil {
		return nil, err
	}
	if filepath.Dir(canonicalRun) != canonicalBase || !strings.HasPrefix(filepath.Base(run), d.Backend+"-") || d.Output != filepath.Join(run, "output.json") || d.Status != filepath.Join(run, "status.json") || d.TaskFile != filepath.Join(run, "task.txt") {
		return nil, errors.New("invalid job paths")
	}
	for _, p := range []string{d.Output, d.Status, d.TaskFile} {
		if resolved, e := filepath.EvalSymlinks(p); e == nil && resolved != filepath.Join(canonicalRun, filepath.Base(p)) {
			return nil, errors.New("job path uses a symlink")
		}
	}
	return &Job{Descriptor: d, path: path}, nil
}

var validID = regexp.MustCompile(`^[0-9]+-[1-9][0-9]*$`)

func sessionID(o Options) string {
	if o.SessionID != "" {
		return o.SessionID
	}
	if v := os.Getenv("VIOLIN_SESSION_ID"); v != "" {
		return v
	}
	return fmt.Sprintf("cli-%d", os.Getpid())
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
	return j.workerMatches()
}
func (j *Job) workerMatches() bool {
	pid := j.Descriptor.PID
	if pid <= 0 {
		return false
	}
	group, err := syscall.Getpgid(pid)
	if err != nil || group != pid {
		return false
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := string(output)
	taskArg := regexp.MustCompile(`(?:^|\s)--task-file\s+` + regexp.QuoteMeta(j.Descriptor.TaskFile) + `(?:\s|$)`)
	return taskArg.MatchString(strings.TrimSpace(command)) && (strings.Contains(command, " worker ") || strings.Contains(command, "violin-worker"))
}
func (j *Job) Live() map[string]any {
	observation := j.observe()
	return map[string]any{"agent_id": j.Descriptor.AgentID, "status": "running", "selected_backend": j.Descriptor.Backend, "requested_backend": j.Descriptor.RequestedBackend, "evidence": j.Descriptor.Evidence, "effective_timeout_seconds": j.Descriptor.Timeout, "timeout_source": j.Descriptor.TimeoutSource, "idle_timeout_seconds": j.Descriptor.IdleTimeout, "idle_timeout_enabled": j.Descriptor.IdleEnabled, "laya_mode": j.Descriptor.LayaMode, "laya_fallback": j.Descriptor.LayaFallback, "laya_model_version": j.Descriptor.LayaModelVersion, "laya_decision": j.Descriptor.LayaDecision, "supervisor_mode": j.Descriptor.SupervisorMode, "supervisor_state": observation.State, "supervisor_action": observation.Action, "supervisor_reason": observation.Reason, "supervisor_updated_at": observation.UpdatedAt, "supervisor_last_event_at": observation.LastEvent, "supervisor_event_count": observation.EventCount}
}

func (j *Job) observe() supervisor.Observation {
	data, err := os.ReadFile(j.Descriptor.Status)
	if err != nil {
		return supervisor.Observe(supervisor.Status{}, j.alive(), time.Now(), supervisorStale(j.Descriptor))
	}
	status, err := supervisor.Parse(data)
	if err != nil {
		return supervisor.Observe(supervisor.Status{}, j.alive(), time.Now(), supervisorStale(j.Descriptor))
	}
	return supervisor.Observe(status, j.alive(), time.Now(), supervisorStale(j.Descriptor))
}

func supervisorStale(d Descriptor) time.Duration {
	if d.SupervisorStale > 0 {
		return time.Duration(d.SupervisorStale) * time.Second
	}
	return 15 * time.Second
}

func (j *Job) Wait(seconds int) (any, error) {
	if seconds < 0 {
		return nil, errors.New("wait_seconds must be nonnegative")
	}
	if seconds > 0 {
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
	observation := j.observe()
	value["supervisor_mode"] = j.Descriptor.SupervisorMode
	value["supervisor_state"] = observation.State
	value["supervisor_action"] = observation.Action
	value["supervisor_reason"] = observation.Reason
	value["supervisor_event_count"] = observation.EventCount
	value["agent_id"] = j.Descriptor.AgentID
	value["selected_backend"] = j.Descriptor.Backend
	value["requested_backend"] = j.Descriptor.RequestedBackend
	value["laya_mode"] = j.Descriptor.LayaMode
	value["laya_fallback"] = j.Descriptor.LayaFallback
	value["laya_model_version"] = j.Descriptor.LayaModelVersion
	value["laya_decision"] = j.Descriptor.LayaDecision
	_ = laya.AppendFeedback(filepath.Dir(j.Descriptor.Evidence), laya.FeedbackEvent{
		AgentID: j.Descriptor.AgentID, RequestedBackend: j.Descriptor.RequestedBackend, SelectedBackend: j.Descriptor.Backend,
		Mode: j.Descriptor.Mode, LayaMode: j.Descriptor.LayaMode, ModelVersion: j.Descriptor.LayaModelVersion,
		Fallback: j.Descriptor.LayaFallback, Confidence: decisionConfidence(j.Descriptor.LayaDecision), Risk: decisionRisk(j.Descriptor.LayaDecision),
		CostTier: decisionCost(j.Descriptor.LayaDecision), LatencyTier: decisionLatency(j.Descriptor.LayaDecision),
		Outcome: reportOutcome(value), DurationSeconds: reportDuration(value), TimeoutSeconds: j.Descriptor.Timeout,
		ErrorClass: reportErrorClass(value),
	})
	_ = os.Remove(j.path)
	return value, nil
}

func decisionConfidence(decision *laya.Decision) float64 {
	if decision == nil {
		return 0
	}
	return decision.Confidence
}

func decisionRisk(decision *laya.Decision) laya.Risk {
	if decision == nil {
		return ""
	}
	return decision.Risk
}

func decisionCost(decision *laya.Decision) laya.Tier {
	if decision == nil {
		return ""
	}
	return decision.CostTier
}

func decisionLatency(decision *laya.Decision) laya.Tier {
	if decision == nil {
		return ""
	}
	return decision.LatencyTier
}

func reportOutcome(value map[string]any) string {
	if status, ok := value["status"].(string); ok {
		return status
	}
	return "unknown"
}

func reportDuration(value map[string]any) float64 {
	if duration, ok := value["duration_seconds"].(float64); ok {
		return duration
	}
	return 0
}

func reportErrorClass(value map[string]any) string {
	if status := reportOutcome(value); status == "completed" {
		return ""
	}
	if phase, ok := value["phase"].(string); ok && phase != "" {
		return phase
	}
	return reportOutcome(value)
}
func (j *Job) Interrupt() (map[string]any, error) {
	if !j.workerMatches() {
		return nil, errors.New("job is stale: worker identity cannot be verified")
	}
	if err := syscall.Kill(-j.Descriptor.PID, syscall.SIGINT); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(8 * time.Second)
	for j.workerMatches() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if j.workerMatches() {
		_ = syscall.Kill(-j.Descriptor.PID, syscall.SIGKILL)
		time.Sleep(100 * time.Millisecond)
	}
	if j.workerMatches() {
		return nil, errors.New("worker remains active after interrupt")
	}
	outputData, outputErr := os.ReadFile(j.Descriptor.Output)
	var outputValue map[string]any
	if outputErr != nil || len(outputData) == 0 || json.Unmarshal(outputData, &outputValue) != nil || outputValue["status"] == nil {
		value := map[string]any{
			"status": "interrupted", "backend": j.Descriptor.Backend, "exit_code": 130,
			"phase": "interrupted", "evidence": j.Descriptor.Evidence,
			"effective_timeout_seconds": j.Descriptor.Timeout, "timeout_source": j.Descriptor.TimeoutSource,
			"idle_timeout_seconds": j.Descriptor.IdleTimeout, "idle_timeout_enabled": j.Descriptor.IdleEnabled,
			"metadata_status": "not_applicable", "smoke_status": "not_applicable",
			"final_message_seen": false, "changed_files": []string{}, "git_diff_check": map[string]any{"status": "not_run"},
			"error_message": "worker interrupted", "summary": "", "supervisor_review_required": true,
		}
		data, _ := json.Marshal(value)
		_ = os.WriteFile(j.Descriptor.Output, data, 0600)
		_ = os.WriteFile(filepath.Join(j.Descriptor.Evidence, "report.json"), data, 0600)
		status := map[string]any{"phase": "interrupted", "pid": j.Descriptor.PID, "evidence": j.Descriptor.Evidence, "effective_timeout_seconds": j.Descriptor.Timeout, "timeout_source": j.Descriptor.TimeoutSource, "idle_timeout_seconds": j.Descriptor.IdleTimeout, "idle_timeout_enabled": j.Descriptor.IdleEnabled}
		statusData, _ := json.Marshal(status)
		_ = os.WriteFile(j.Descriptor.Status, statusData, 0600)
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
func InterruptOwned(root string, ownerInstance string) {
	entries, err := os.ReadDir(filepath.Join(root, "jobs"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := Open(root, strings.TrimSuffix(entry.Name(), ".json"))
		if err == nil && ownerInstance != "" && job.Descriptor.OwnerInstance == ownerInstance && job.alive() {
			_, _ = job.Interrupt()
		}
	}
}

func selectBackend(root string, order []string, backendConfig map[string]config.Backend, preferred []string, scheduler config.Scheduler, session string, advance bool) (string, error) {
	if len(order) == 0 {
		order = []string{"agy", "qwen", "claude"}
	}
	counts := map[string]int{}
	machine, sessionCount := 0, 0
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
			machine++
			if job.Descriptor.SessionID == session {
				sessionCount++
			}
		}
	}
	if machine >= scheduler.MachineMaxConcurrency || sessionCount >= scheduler.SessionMaxConcurrency {
		return "", errors.New("worker capacity is full")
	}
	for _, candidate := range preferred {
		if !contains(order, candidate) || counts[candidate] >= capacity(candidate, backendConfig) {
			continue
		}
		if advance {
			for i, name := range order {
				if name == candidate {
					data, _ := json.Marshal(map[string]int{"index": (i + 1) % len(order)})
					_ = os.WriteFile(filepath.Join(root, "round-robin.json"), data, 0600)
					break
				}
			}
		}
		return candidate, nil
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
		limit := capacity(candidate, backendConfig)
		if counts[candidate] < limit {
			next := (index + offset + 1) % len(order)
			if advance {
				data, _ := json.Marshal(map[string]int{"index": next})
				_ = os.WriteFile(statePath, data, 0600)
			}
			return candidate, nil
		}
	}
	return "", errors.New("worker capacity is full")
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func capacity(name string, backendConfig map[string]config.Backend) int {
	limit := backendConfig[name].MaxConcurrency
	if limit == 0 {
		return 1
	}
	return limit
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

func EvaluateLaya(root, task, mode, requested string, cfg config.Config) laya.Result {
	manager, err := models.NewManager(filepath.Join(root, "models"))
	if err != nil {
		return laya.Result{Fallback: true, Error: err.Error()}
	}
	modelPath, _ := manager.ActivePath()
	runner := cfg.Laya.Runner
	if len(runner) == 0 {
		runner = laya.RunnerFromEnv()
	}
	engine := laya.ManagedEngine{Manager: manager, Runner: runner, ModelPath: modelPath, Timeout: time.Duration(cfg.Laya.TimeoutSeconds) * time.Second, Fallback: laya.FallbackEngine{}}
	result, _ := engine.Evaluate(laya.Request{Language: laya.ProtocolLanguage, State: map[string]any{"task": task, "mode": mode}, Questions: []laya.Question{{ID: "backend", Kind: laya.Choice, Options: []string{"agy", "qwen", "claude"}, Fallback: requested}}})
	return result
}
