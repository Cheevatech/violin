package mcp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/film/violin/internal/auth"
	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
	"github.com/film/violin/internal/health"
	"github.com/film/violin/internal/jobs"
)

type request struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}
type response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

func Run(in io.Reader, out io.Writer) error {
	root := workerRoot()
	identity := make([]byte, 16)
	if _, err := rand.Read(identity); err != nil {
		return err
	}
	ownerInstance = fmt.Sprintf("%x", identity)
	defer jobs.InterruptOwned(root, ownerInstance)
	s := bufio.NewScanner(in)
	enc := json.NewEncoder(out)
	for s.Scan() {
		var req request
		if err := json.Unmarshal(s.Bytes(), &req); err != nil {
			continue
		}
		r := response{JSONRPC: "2.0", ID: req.ID}
		var value any
		var err error
		switch req.Method {
		case "initialize":
			value = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "violin", "version": "2.0.0"}}
		case "ping":
			value = map[string]any{}
		case "tools/list":
			value = tools()
		case "tools/call":
			value, err = call(req.Params)
		default:
			err = fmt.Errorf("method not found")
		}
		if err != nil && req.Method == "tools/call" {
			r.Result = map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": err.Error()}}}
		} else if err != nil {
			r.Error = map[string]any{"code": -32602, "message": err.Error()}
		} else if req.Method == "tools/call" {
			data, _ := json.Marshal(value)
			r.Result = map[string]any{"content": []map[string]string{{"type": "text", "text": string(data)}}}
		} else {
			r.Result = value
		}
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return s.Err()
}

var ownerInstance string

func tools() map[string]any {
	object := func(properties map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	return map[string]any{"tools": []map[string]any{
		{"name": "spawn_agent", "description": "Delegate a bounded task to an external coding agent.", "inputSchema": object(map[string]any{"backend": map[string]any{"type": "string", "enum": []string{"auto", "agy", "qwen", "claude"}}, "task": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string", "enum": []string{"inspect", "implement"}}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1}, "idle_timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600}}, []string{"task", "cwd"})},
		{"name": "wait_agent", "description": "Wait on an existing agent.", "inputSchema": object(map[string]any{"agent_id": map[string]any{"type": "string"}, "wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 50}}, []string{"agent_id"})},
		{"name": "list_agents", "description": "List agent jobs.", "inputSchema": object(map[string]any{}, nil)},
		{"name": "interrupt_agent", "description": "Interrupt an agent without reverting work.", "inputSchema": object(map[string]any{"agent_id": map[string]any{"type": "string"}}, []string{"agent_id"})},
		{"name": "laya_route", "description": "Use the installed Laya model to recommend routing, timeout, risk, retry, and execution policy.", "inputSchema": object(map[string]any{"task": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string", "enum": []string{"inspect", "implement"}}, "backend": map[string]any{"type": "string", "enum": []string{"auto", "agy", "qwen", "claude"}}}, []string{"task"})},
		{"name": "laya_review_risk", "description": "Review task risk before allowing an implementation, without changing files or spawning a worker.", "inputSchema": object(map[string]any{"task": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string", "enum": []string{"inspect", "implement"}}}, []string{"task"})},
		{"name": "laya_check_job", "description": "Inspect a Violin worker lifecycle state and supervisor evidence.", "inputSchema": object(map[string]any{"agent_id": map[string]any{"type": "string"}}, []string{"agent_id"})},
		{"name": "laya_wait_job", "description": "Wait for a Violin worker once and return its terminal or current supervisor state.", "inputSchema": object(map[string]any{"agent_id": map[string]any{"type": "string"}, "wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 50}}, []string{"agent_id"})},
		{"name": "laya_explain_decision", "description": "Return the Laya decision and model metadata recorded for a worker.", "inputSchema": object(map[string]any{"agent_id": map[string]any{"type": "string"}}, []string{"agent_id"})},
		{"name": "auth_status", "description": "Inspect global provider authentication without exposing credentials.", "inputSchema": object(map[string]any{}, nil)},
		{"name": "health_status", "description": "Run configured provider health checks without exposing credentials.", "inputSchema": object(
			map[string]any{"provider": map[string]any{"type": "string", "enum": []string{"qwen", "agy", "claude", "all"}}},
			[]string{"provider"},
		)},
	}}
}
func call(params map[string]any) (any, error) {
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	root := workerRoot()
	switch name {
	case "auth_status":
		settings, err := config.LoadFor("")
		if err != nil {
			return nil, err
		}
		return auth.NewManager(credentials.Default()).AllStatusWithConfig(context.Background(), settings)
	case "health_status":
		provider, _ := args["provider"].(string)
		settings, err := config.LoadFor("")
		if err != nil {
			return nil, err
		}
		if provider == "all" {
			result := map[string]health.Result{}
			for _, name := range []string{"qwen", "agy", "claude"} {
				result[name] = health.Check(context.Background(), settings, name, credentials.Default())
			}
			return result, nil
		}
		if provider != "qwen" && provider != "agy" && provider != "claude" {
			return nil, fmt.Errorf("unknown provider %q", provider)
		}
		return health.Check(context.Background(), settings, provider, credentials.Default()), nil
	case "spawn_agent":
		cwd, _ := args["cwd"].(string)
		task, _ := args["task"].(string)
		backend, _ := args["backend"].(string)
		mode, _ := args["mode"].(string)
		timeout, timeoutSource := intArg(args, "timeout_seconds")
		idle, _ := intArg(args, "idle_timeout_seconds")
		job, err := jobs.Spawn(jobs.Options{Root: root, Workspace: cwd, Backend: backend, RequestedBackend: backend, Mode: mode, Task: task, Timeout: timeout, TimeoutSource: timeoutSource, IdleTimeout: idle, OwnerInstance: ownerInstance})
		if err != nil {
			if required, ok := err.(jobs.AuthRequiredError); ok {
				return required.Details(), nil
			}
			return nil, err
		}
		return job.Live(), nil
	case "wait_agent":
		seconds, err := waitArg(args)
		if err != nil {
			return nil, err
		}
		id, _ := args["agent_id"].(string)
		job, err := jobs.Open(root, id)
		if err != nil {
			return nil, err
		}
		return job.Wait(seconds)
	case "list_agents":
		return jobs.List(root)
	case "interrupt_agent":
		id, _ := args["agent_id"].(string)
		job, err := jobs.Open(root, id)
		if err != nil {
			return nil, err
		}
		return job.Interrupt()
	case "laya_route":
		task, _ := args["task"].(string)
		mode := stringArg(args, "mode", "inspect")
		requested := stringArg(args, "backend", "auto")
		workspace := stringArg(args, "cwd", "")
		settings, err := config.LoadFor(workspace)
		if err != nil {
			return nil, err
		}
		return jobs.EvaluateLaya(root, task, mode, requested, settings), nil
	case "laya_review_risk":
		task, _ := args["task"].(string)
		mode := stringArg(args, "mode", "implement")
		settings, err := config.LoadFor("")
		if err != nil {
			return nil, err
		}
		result := jobs.EvaluateLaya(root, task, mode, "auto", settings)
		if result.Decision == nil {
			return result, nil
		}
		return map[string]any{"risk": result.Decision.Risk, "task_mode": result.Decision.TaskMode, "confidence": result.Decision.Confidence, "margin": result.Decision.Margin, "reason_codes": result.Decision.ReasonCodes, "model_version": result.ModelVersion, "fallback": result.Fallback, "decision": result.Decision}, nil
	case "laya_check_job":
		job, err := jobs.Open(root, stringArg(args, "agent_id", ""))
		if err != nil {
			return nil, err
		}
		return job.Live(), nil
	case "laya_wait_job":
		seconds, err := waitArg(args)
		if err != nil {
			return nil, err
		}
		job, err := jobs.Open(root, stringArg(args, "agent_id", ""))
		if err != nil {
			return nil, err
		}
		return job.Wait(seconds)
	case "laya_explain_decision":
		job, err := jobs.Open(root, stringArg(args, "agent_id", ""))
		if err != nil {
			return nil, err
		}
		return map[string]any{"agent_id": job.Descriptor.AgentID, "laya_mode": job.Descriptor.LayaMode, "laya_fallback": job.Descriptor.LayaFallback, "laya_model_version": job.Descriptor.LayaModelVersion, "laya_error": job.Descriptor.LayaError, "laya_decision": job.Descriptor.LayaDecision}, nil
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func waitArg(args map[string]any) (int, error) {
	value, ok := args["wait_seconds"]
	if !ok {
		return 50, nil
	}
	var n int
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < 0 {
			return 0, errors.New("wait_seconds must be a nonnegative integer")
		}
		n = int(v)
	case int:
		if v < 0 {
			return 0, errors.New("wait_seconds must be a nonnegative integer")
		}
		n = v
	default:
		return 0, errors.New("wait_seconds must be a nonnegative integer")
	}
	if n > 50 {
		n = 50
	}
	return n, nil
}

func stringArg(args map[string]any, key, fallback string) string {
	if value, ok := args[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func workerRoot() string {
	if root := os.Getenv("VIOLIN_WORKER_RUNS"); root != "" {
		return root
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "violin-workers")
}

func intArg(args map[string]any, key string) (int, string) {
	value, ok := args[key]
	if !ok {
		return 0, ""
	}
	switch typed := value.(type) {
	case float64:
		return int(typed), "request"
	case int:
		return typed, "request"
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed, "request"
		}
	}
	return 0, ""
}
