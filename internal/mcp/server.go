package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

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
func tools() map[string]any {
	object := func(properties map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	return map[string]any{"tools": []map[string]any{
		{"name": "spawn_agent", "description": "Delegate a bounded task to an external coding agent.", "inputSchema": object(map[string]any{"backend": map[string]any{"type": "string", "enum": []string{"auto", "agy", "qwen", "claude"}}, "task": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string", "enum": []string{"inspect", "implement"}}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1}, "idle_timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600}}, []string{"task", "cwd"})},
		{"name": "wait_agent", "description": "Wait on an existing agent.", "inputSchema": object(map[string]any{"agent_id": map[string]any{"type": "string"}, "wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 50}}, []string{"agent_id"})},
		{"name": "list_agents", "description": "List agent jobs.", "inputSchema": object(map[string]any{}, nil)},
		{"name": "interrupt_agent", "description": "Interrupt an agent without reverting work.", "inputSchema": object(map[string]any{"agent_id": map[string]any{"type": "string"}}, []string{"agent_id"})},
	}}
}
func call(params map[string]any) (any, error) {
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	root := os.Getenv("VIOLIN_WORKER_RUNS")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".local", "state", "violin-workers")
	}
	switch name {
	case "spawn_agent":
		cwd, _ := args["cwd"].(string)
		task, _ := args["task"].(string)
		backend, _ := args["backend"].(string)
		mode, _ := args["mode"].(string)
		timeout, timeoutSource := intArg(args, "timeout_seconds")
		idle, _ := intArg(args, "idle_timeout_seconds")
		job, err := jobs.Spawn(jobs.Options{Root: root, Workspace: cwd, Backend: backend, RequestedBackend: backend, Mode: mode, Task: task, Timeout: timeout, TimeoutSource: timeoutSource, IdleTimeout: idle})
		if err != nil {
			return nil, err
		}
		return job.Live(), nil
	case "wait_agent":
		id, _ := args["agent_id"].(string)
		job, err := jobs.Open(root, id)
		if err != nil {
			return nil, err
		}
		return job.Wait(50)
	case "list_agents":
		return jobs.List(root)
	case "interrupt_agent":
		id, _ := args["agent_id"].(string)
		job, err := jobs.Open(root, id)
		if err != nil {
			return nil, err
		}
		return job.Interrupt()
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
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
