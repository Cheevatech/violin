package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
	"github.com/film/violin/internal/providers"
)

type Options struct {
	Backend, Mode, Workspace, TaskFile string
	Timeout, IdleTimeout               int
	IdleTimeoutEnabled                 bool
	baseline                           []string
}

type executionError struct {
	status string
	err    error
}

func idleChannel(timer *time.Timer) <-chan time.Time {
	if timer == nil {
		return nil
	}
	return timer.C
}

func (e executionError) Error() string { return e.err.Error() }
func (e executionError) Unwrap() error { return e.err }

func Parse(args []string) (Options, error) {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	mode := fs.String("mode", "inspect", "mode")
	workspace := fs.String("C", ".", "workspace")
	taskFile := fs.String("task-file", "", "task file")
	timeout := fs.Int("timeout", 900, "timeout seconds")
	idleTimeout := fs.Int("idle-timeout", 300, "idle timeout seconds")
	if len(args) == 0 {
		return Options{}, errors.New("worker requires backend")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return Options{}, err
	}
	if *taskFile == "" {
		return Options{}, errors.New("worker requires --task-file")
	}
	workspacePath, err := filepath.Abs(*workspace)
	if err != nil {
		return Options{}, err
	}
	return Options{Backend: args[0], Mode: *mode, Workspace: workspacePath, TaskFile: *taskFile, Timeout: *timeout, IdleTimeout: *idleTimeout}, nil
}

func Run(ctx context.Context, options Options) error {
	if options.Timeout < 1 {
		return errors.New("worker timeout must be positive")
	}
	task, err := os.ReadFile(options.TaskFile)
	if err != nil {
		return err
	}
	settings, err := config.LoadFor(options.Workspace)
	if err != nil {
		return err
	}
	backend := settings.Backend[options.Backend]
	options.baseline = gitFiles(options.Workspace)
	started := time.Now()
	writeStatus("starting", options, started)
	writeStatus("running", options, started)
	var text string
	var usage any
	cliCommand := backend.CLI.Command
	if len(cliCommand) == 0 {
		cliCommand = commandParts(backend.Command)
	}
	options.IdleTimeoutEnabled = true
	if (backend.Transport == "api" || (backend.Transport == "auto" && len(cliCommand) == 0)) && (options.Backend == "qwen" || options.Backend == "agy") {
		options.IdleTimeoutEnabled = false
	}
	if backend.Transport == "api" || (backend.Transport == "auto" && len(cliCommand) == 0) {
		provider, err := providers.FromConfigProvider(ctx, settings, credentials.Default(), options.Backend)
		if err != nil {
			return writeFailure(options, started, err)
		}
		result, err := provider.Execute(ctx, providers.Request{Task: string(task), Workspace: options.Workspace, Mode: options.Mode, Timeout: time.Duration(options.Timeout) * time.Second})
		if err != nil {
			return writeFailure(options, started, err)
		}
		text, usage = result.Text, result.Usage
	} else {
		text, err = runCLI(ctx, cliCommand, string(task), options)
		if err != nil {
			return writeFailure(options, started, err)
		}
	}
	if strings.TrimSpace(text) == "" {
		return writeFailure(options, started, errors.New("provider returned an empty response"))
	}
	writeStatus("completed", options, started)
	return writeReport(options, started, "completed", 0, text, usage, "")
}

func commandParts(value any) []string {
	switch typed := value.(type) {
	case string:
		return strings.Fields(typed)
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, fmt.Sprint(item))
		}
		return result
	default:
		return nil
	}
}

func runCLI(parent context.Context, command []string, task string, options Options) (string, error) {
	if len(command) == 0 {
		return "", errors.New("CLI transport is not configured")
	}
	values := map[string]string{"workspace": options.Workspace, "task_file": options.TaskFile, "mode": options.Mode, "timeout": fmt.Sprint(options.Timeout)}
	argv := make([]string, len(command))
	for i, value := range command {
		for key, replacement := range values {
			value = strings.ReplaceAll(value, "{"+key+"}", replacement)
		}
		argv[i] = value
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Stdin = options.Workspace, strings.NewReader(task)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("%s: %w", argv[0], err)
	}
	type streamResult struct {
		data   []byte
		stdout bool
		err    error
	}
	streams := make(chan streamResult, 2)
	activity := make(chan struct{}, 32)
	read := func(reader io.Reader, isStdout bool) {
		var data []byte
		buffer := make([]byte, 4096)
		var readErr error
		for {
			count, err := reader.Read(buffer)
			if count > 0 {
				data = append(data, buffer[:count]...)
				select {
				case activity <- struct{}{}:
				default:
				}
			}
			if err != nil {
				if err != io.EOF {
					readErr = err
				}
				break
			}
		}
		streams <- streamResult{data: data, stdout: isStdout, err: readErr}
	}
	go read(stdout, true)
	go read(stderr, false)
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var waitCh <-chan error = wait
	var waitErr error
	hard := time.NewTimer(time.Duration(options.Timeout) * time.Second)
	defer hard.Stop()
	var idle *time.Timer
	if options.IdleTimeoutEnabled && options.IdleTimeout > 0 {
		idle = time.NewTimer(time.Duration(options.IdleTimeout) * time.Second)
		defer idle.Stop()
	}
	var output, errorOutput []byte
	for completed := 0; completed < 2 || waitCh != nil; {
		select {
		case result := <-streams:
			completed++
			if result.stdout {
				output = result.data
			} else {
				errorOutput = result.data
			}
			if idle != nil {
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(time.Duration(options.IdleTimeout) * time.Second)
			}
		case <-activity:
			if idle != nil {
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(time.Duration(options.IdleTimeout) * time.Second)
			}
		case err := <-waitCh:
			waitErr = err
			waitCh = nil
		case <-parent.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return "", executionError{status: "interrupted", err: parent.Err()}
		case <-hard.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return "", executionError{status: "timeout", err: fmt.Errorf("worker exceeded timeout of %d seconds", options.Timeout)}
		case <-idleChannel(idle):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return "", executionError{status: "idle_timeout", err: fmt.Errorf("worker exceeded idle timeout of %d seconds", options.IdleTimeout)}
		}
	}
	if waitErr != nil {
		if len(errorOutput) > 0 {
			return "", executionError{status: "provider_error", err: fmt.Errorf("%s: %w: %s", argv[0], waitErr, strings.TrimSpace(string(errorOutput)))}
		}
		return "", executionError{status: "provider_error", err: fmt.Errorf("%s: %w", argv[0], waitErr)}
	}
	return parseProviderOutput(bytes.TrimSpace(append(output, errorOutput...)), options.Backend)
}

func parseCLIOutput(data []byte) string {
	text, err := parseProviderOutput(data, "custom")
	if err != nil {
		return strings.TrimSpace(string(data))
	}
	return text
}

// parseProviderOutput accepts both one-shot JSON and JSONL event streams used
// by Qwen/AGY/Claude CLIs. It deliberately keeps protocol interpretation at
// the worker boundary so providers can evolve without changing job lifecycle.
func parseProviderOutput(data []byte, backend string) (string, error) {
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", errors.New("provider returned an empty response")
	}
	var events []map[string]any
	if json.Unmarshal([]byte(value), &events) != nil {
		for _, line := range strings.Split(value, "\n") {
			var event map[string]any
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &event) == nil {
				events = append(events, event)
			}
		}
	}
	if len(events) == 0 {
		var payload map[string]any
		if json.Unmarshal([]byte(value), &payload) != nil {
			return value, nil
		}
		events = []map[string]any{payload}
	}
	var textParts []string
	var final string
	for _, event := range events {
		kind, _ := event["type"].(string)
		if kind == "result" {
			if failed, _ := event["is_error"].(bool); failed || strings.HasPrefix(strings.ToLower(fmt.Sprint(event["subtype"])), "error") {
				return "", fmt.Errorf("%s provider error: %s", backend, eventText(event))
			}
		}
		if kind == "response.completed" {
			if response, ok := event["response"].(map[string]any); ok {
				if status, _ := response["status"].(string); status == "failed" || status == "incomplete" {
					return "", fmt.Errorf("%s provider response %s: %s", backend, status, eventText(response))
				}
			}
		}
		candidate := eventText(event)
		if kind == "item.completed" {
			if item, ok := event["item"].(map[string]any); ok && item["type"] != "agent_message" && item["type"] != "message" {
				candidate = ""
			}
		}
		if strings.TrimSpace(candidate) != "" {
			if kind == "result" || kind == "response.completed" {
				final = candidate
			} else {
				textParts = append(textParts, candidate)
			}
		}
	}
	if strings.TrimSpace(final) != "" {
		return final, nil
	}
	if len(textParts) > 0 {
		return strings.Join(textParts, ""), nil
	}
	return "", fmt.Errorf("%s provider returned no final text", backend)
}

func eventText(event map[string]any) string {
	for _, key := range []string{"response", "result", "output_text", "output", "text", "content"} {
		if text, ok := event[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	if item, ok := event["item"].(map[string]any); ok {
		if text := eventText(item); text != "" {
			return text
		}
	}
	if response, ok := event["response"].(map[string]any); ok {
		if text := eventText(response); text != "" {
			return text
		}
	}
	if values, ok := event["content"].([]any); ok {
		var parts []string
		for _, value := range values {
			if part, ok := value.(map[string]any); ok {
				parts = append(parts, eventText(part))
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func writeFailure(options Options, started time.Time, err error) error {
	status := "provider_error"
	var execution executionError
	if errors.As(err, &execution) {
		status = execution.status
	}
	writeStatus(status, options, started)
	_ = writeReport(options, started, status, 1, "", nil, err.Error())
	return err
}

func writeReport(options Options, started time.Time, status string, exitCode int, text string, usage any, errorMessage string) error {
	run := os.Getenv("VIOLIN_WORKER_EVIDENCE")
	if run == "" {
		run = filepath.Dir(options.TaskFile)
	}
	changed := changedFiles(options.Workspace, options.baseline)
	diffCheck := gitDiffCheck(options.Workspace)
	idleTimeout := options.IdleTimeout
	if idleTimeout < 1 {
		idleTimeout = 1
	}
	idleEnabled := options.IdleTimeoutEnabled
	const summaryLimit = 6000
	summary := text
	if len(summary) > summaryLimit {
		summary = summary[:summaryLimit]
	}
	report := map[string]any{"status": status, "backend": options.Backend, "exit_code": exitCode, "duration_seconds": time.Since(started).Seconds(), "evidence": run, "phase": status, "metadata_status": "not_applicable", "smoke_status": "not_applicable", "final_message_seen": strings.TrimSpace(text) != "", "changed_files": changed, "git_diff_check": diffCheck, "effective_timeout_seconds": options.Timeout, "timeout_source": timeoutSource(options), "idle_timeout_seconds": idleTimeout, "idle_timeout_enabled": idleEnabled, "summary": summary, "summary_truncated": len(text) > summaryLimit, "supervisor_review_required": true, "usage": usage}
	if errorMessage != "" {
		report["error_message"] = errorMessage
	}
	statusPath := filepath.Join(run, "report.json")
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if statusPath != "" {
		_ = os.WriteFile(statusPath, data, 0600)
	}
	_, err = io.WriteString(os.Stdout, string(data)+"\n")
	return err
}

func timeoutSource(options Options) string {
	if source := os.Getenv("VIOLIN_TIMEOUT_SOURCE"); source != "" {
		return source
	}
	return "explicit"
}

func gitFiles(workspace string) []string {
	output, err := exec.Command("git", "-C", workspace, "status", "--short").Output()
	if err != nil {
		return []string{}
	}
	var result []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > 3 {
			result = append(result, strings.TrimSpace(line[3:]))
		} else {
			result = append(result, strings.TrimSpace(line))
		}
	}
	return result
}

func changedFiles(workspace string, baseline []string) []string {
	current := gitFiles(workspace)
	before := make(map[string]bool, len(baseline))
	for _, file := range baseline {
		before[file] = true
	}
	var changed []string
	for _, file := range current {
		if !before[file] {
			changed = append(changed, file)
		}
	}
	return changed
}

func gitDiffCheck(workspace string) map[string]any {
	if err := exec.Command("git", "-C", workspace, "diff", "--check").Run(); err != nil {
		return map[string]any{"status": "failed"}
	}
	return map[string]any{"status": "passed"}
}

func writeStatus(phase string, options Options, started time.Time) {
	path := os.Getenv("VIOLIN_WORKER_STATUS")
	if path == "" {
		return
	}
	idleTimeout := options.IdleTimeout
	if idleTimeout < 1 {
		idleTimeout = 1
	}
	idleEnabled := options.IdleTimeoutEnabled
	data, _ := json.Marshal(map[string]any{"phase": phase, "pid": os.Getpid(), "elapsed_seconds": time.Since(started).Seconds(), "evidence": os.Getenv("VIOLIN_WORKER_EVIDENCE"), "effective_timeout_seconds": options.Timeout, "timeout_source": timeoutSource(options), "idle_timeout_seconds": idleTimeout, "idle_timeout_enabled": idleEnabled})
	_ = os.WriteFile(path, data, 0600)
}
