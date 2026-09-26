package laya

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	LocalModelRepo     = "ggml-org/Qwen3-4B-GGUF"
	LocalModelQuant    = "Q4_K_M"
	LocalModelRevision = "2f3b082b1356a6123f7ed71e65aea340da25d53c"
	LocalModelFile     = "Qwen3-4B-Q4_K_M.gguf"
	LocalModelSHA256   = "ab27b9bfa375a178d6cba48f3ad892b94b7739659dcc7aae8058ce0ffed6b328"
	LocalModelBytes    = int64(2497280640)
	LocalModelVersion  = "qwen3-4b-q4km-2f3b082"
)

type LocalLLMStatus struct {
	Installed  bool   `json:"installed"`
	Model      string `json:"model"`
	Version    string `json:"model_version"`
	Quant      string `json:"quantization"`
	Runtime    string `json:"runtime"`
	ModelPath  string `json:"model_path,omitempty"`
	ModelBytes int64  `json:"model_bytes,omitempty"`
	Verified   bool   `json:"verified"`
	Error      string `json:"error,omitempty"`
}

type LocalLLM struct {
	cmd        *exec.Cmd
	done       chan struct{}
	processErr error
	client     *http.Client
	endpoint   string
	apiKey     string
	modelPath  string
	version    string
	logFile    *os.File
	closeOnce  sync.Once
}

type localCompletionRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	Temperature    float64        `json:"temperature"`
	MaxTokens      int            `json:"max_tokens"`
	ResponseFormat map[string]any `json:"response_format"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type localCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// EnsureLocalLLM installs llama.cpp when needed and warms the pinned local model
// into Violin's shared state directory. Model weights stay outside the binary.
func EnsureLocalLLM(ctx context.Context, root string) error {
	binary, err := findLlamaServer()
	if err != nil {
		if runtime.GOOS != "darwin" {
			return errors.New("Laya local LLM requires llama-server; install llama.cpp and rerun violin install")
		}
		brew, brewErr := exec.LookPath("brew")
		if brewErr != nil {
			return errors.New("Laya local LLM requires Homebrew to install llama.cpp")
		}
		install := exec.CommandContext(ctx, brew, "install", "llama.cpp")
		install.Stdout, install.Stderr = os.Stdout, os.Stderr
		if err := install.Run(); err != nil {
			return fmt.Errorf("install llama.cpp: %w", err)
		}
		binary, err = findLlamaServer()
		if err != nil {
			return err
		}
	}

	modelPath := localModelPath(root)
	if err := ensureLocalModel(ctx, root, modelPath); err != nil {
		return err
	}
	server, err := startLocalServer(ctx, root, binary, "--model", modelPath)
	if err != nil {
		return fmt.Errorf("download and load Laya model: %w", err)
	}
	_ = server.Close()
	return nil
}

func GetLocalLLMStatus(root string) LocalLLMStatus {
	result := LocalLLMStatus{Model: LocalModelRepo, Version: LocalModelVersion, Quant: LocalModelQuant, Runtime: "llama.cpp Metal"}
	binary, err := findLlamaServer()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Runtime = binary
	result.ModelPath = localModelPath(root)
	info, err := os.Stat(result.ModelPath)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.ModelBytes = info.Size()
	result.Installed = true
	if err := verifyLocalModel(result.ModelPath); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Verified = true
	return result
}

// StartLocalLLM starts the verified model on loopback. A missing installation
// returns nil so older installations retain their existing safe fallback.
func StartLocalLLM(ctx context.Context, root string) (*LocalLLM, error) {
	status := GetLocalLLMStatus(root)
	if !status.Installed {
		return nil, nil
	}
	if !status.Verified {
		return nil, fmt.Errorf("Laya model verification failed: %s", status.Error)
	}
	server, err := startLocalServer(ctx, root, status.Runtime, "--model", status.ModelPath)
	if err != nil {
		return nil, err
	}
	return server, nil
}

func (e *LocalLLM) Close() error {
	var closeErr error
	e.closeOnce.Do(func() {
		if e.cmd != nil && e.cmd.Process != nil {
			_ = e.cmd.Process.Signal(os.Interrupt)
			select {
			case <-e.done:
			case <-time.After(2 * time.Second):
				_ = e.cmd.Process.Kill()
				<-e.done
			}
		}
		if e.logFile != nil {
			closeErr = e.logFile.Close()
		}
	})
	return closeErr
}

func (e *LocalLLM) Evaluate(request Request) (Result, error) {
	started := time.Now()
	state, _ := request.State.(map[string]any)
	input, _ := json.Marshal(map[string]any{"task": state["task"], "mode": state["mode"], "questions": request.Questions})
	system := `You are Laya, Violin's local routing and risk decision model. Read the task and constraints, then return one compact JSON object only, without markdown or reasoning. Use /no_think. Never claim user approval. Choose backend_candidates only from qwen, agy, claude. Use inspect or implement for task_mode; use high risk for security, credentials, destructive or uncertain work. Explicit user mode takes precedence. Use max_attempts=1; only choose 2 for a bounded inspect retry. Keep timeout_hint_seconds between 300 and 14400. execution_target is exactly local or external and is never a backend name. cost_tier and latency_tier are exactly low, medium, or high. Give conservative independent estimates in head_confidence and head_margin from 0 to 1 for backend, task_mode, risk, timeout_policy, and retry_policy; low values mean uncertain, and never inflate them.`
	body, err := json.Marshal(localCompletionRequest{
		Model: e.version,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: "/no_think\n" + string(input)},
		},
		Temperature:    0,
		MaxTokens:      384,
		ResponseFormat: localDecisionResponseSchema(),
	})
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	response, err := e.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return Result{}, fmt.Errorf("local Laya server returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	var completion localCompletionResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&completion); err != nil {
		return Result{}, err
	}
	if len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return Result{}, errors.New("local Laya model returned an empty decision")
	}
	var decision Decision
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &decision); err != nil {
		return Result{}, fmt.Errorf("decode local Laya decision: %w", err)
	}
	decision.ModelVersion = e.version
	if decision.Retry.MaxAttempts < 1 || decision.Retry.MaxAttempts > 2 {
		decision.Retry.MaxAttempts = 1
	}
	if decision.TaskMode != "inspect" {
		decision.Retry.MaxAttempts = 1
	}
	if len(decision.HeadConfidence) == 0 || len(decision.HeadMargin) == 0 {
		return Result{}, errors.New("local Laya decision omitted head confidence or margin estimates")
	}
	if err := decision.Validate(); err != nil {
		return Result{}, fmt.Errorf("invalid local Laya decision: %w", err)
	}
	return Result{Decision: &decision, ModelVersion: e.version, LatencyMS: float64(time.Since(started).Microseconds()) / 1000}, nil
}

func localDecisionResponseSchema() map[string]any {
	stringEnum := func(values ...string) map[string]any {
		return map[string]any{"type": "string", "enum": values}
	}
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "laya_decision",
			"strict": true,
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"backend_candidates":   map[string]any{"type": "array", "items": stringEnum("qwen", "agy", "claude"), "minItems": 1},
					"task_mode":            stringEnum("inspect", "implement"),
					"risk":                 stringEnum("low", "medium", "high"),
					"timeout_hint_seconds": map[string]any{"type": "integer", "minimum": 300, "maximum": 14400},
					"idle_timeout_enabled": map[string]any{"type": "boolean"},
					"retry_hint": map[string]any{"type": "object", "properties": map[string]any{
						"max_attempts":    map[string]any{"type": "integer", "minimum": 1, "maximum": 2},
						"backoff_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 60},
					}, "required": []string{"max_attempts", "backoff_seconds"}, "additionalProperties": false},
					"execution_target": stringEnum("local", "external"),
					"cost_tier":        stringEnum("low", "medium", "high"),
					"latency_tier":     stringEnum("low", "medium", "high"),
					"confidence":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					"margin":           map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					"head_confidence":  localHeadStatsSchema(),
					"head_margin":      localHeadStatsSchema(),
					"reason_codes":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"model_version":    map[string]any{"type": "string"},
				},
				"required":             []string{"backend_candidates", "task_mode", "risk", "timeout_hint_seconds", "idle_timeout_enabled", "retry_hint", "execution_target", "cost_tier", "latency_tier", "confidence", "margin", "head_confidence", "head_margin", "reason_codes", "model_version"},
				"additionalProperties": false,
			},
		},
	}
}

func localHeadStatsSchema() map[string]any {
	properties := map[string]any{}
	required := []string{"backend", "task_mode", "risk", "timeout_policy", "retry_policy"}
	for _, name := range required {
		properties[name] = map[string]any{"type": "number", "minimum": 0, "maximum": 1}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func startLocalServer(ctx context.Context, root, binary string, modelArgs ...string) (*LocalLLM, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	keyBytes := make([]byte, 24)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, err
	}
	key := hex.EncodeToString(keyBytes)
	dir := filepath.Join(root, "laya")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "llama-server.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	args := append([]string{}, modelArgs...)
	args = append(args, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--n-gpu-layers", "99", "--ctx-size", "8192", "--jinja", "--alias", LocalModelVersion, "--api-key", key, "--cors-origins", "localhost")
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	server := &LocalLLM{cmd: cmd, done: make(chan struct{}), client: &http.Client{Timeout: 45 * time.Second}, endpoint: fmt.Sprintf("http://127.0.0.1:%d", port), apiKey: key, modelPath: localModelPath(root), version: LocalModelVersion, logFile: logFile}
	go func() {
		server.processErr = cmd.Wait()
		close(server.done)
	}()
	deadline := time.Now().Add(8 * time.Minute)
	if len(modelArgs) > 0 && modelArgs[0] == "--model" {
		deadline = time.Now().Add(3 * time.Minute)
	}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			_ = server.Close()
			return nil, ctx.Err()
		case <-server.done:
			_ = server.logFile.Close()
			return nil, fmt.Errorf("llama-server exited during startup (%v); see %s", server.processErr, filepath.Join(dir, "llama-server.log"))
		default:
		}
		if server.ready() {
			return server, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	_ = server.Close()
	return nil, fmt.Errorf("llama-server did not become ready; see %s", filepath.Join(dir, "llama-server.log"))
}

func (e *LocalLLM) ready() bool {
	req, err := http.NewRequest(http.MethodGet, e.endpoint+"/health", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	response, err := e.client.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func localModelPath(root string) string {
	return filepath.Join(root, "laya", "models", LocalModelVersion, LocalModelFile)
}

func ensureLocalModel(ctx context.Context, root, destination string) error {
	if err := verifyLocalModel(destination); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	cacheModel := filepath.Join(root, "laya", "hf-cache", "hub", "models--ggml-org--Qwen3-4B-GGUF", "snapshots", LocalModelRevision, LocalModelFile)
	if verifyLocalModel(cacheModel) == nil {
		if err := os.Link(cacheModel, destination); err == nil {
			return nil
		}
	}
	url := "https://huggingface.co/" + LocalModelRepo + "/resolve/" + LocalModelRevision + "/" + LocalModelFile
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("download Laya model: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download Laya model: HTTP %d", response.StatusCode)
	}
	tmp := destination + ".download"
	output, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, io.LimitReader(response.Body, LocalModelBytes+1))
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := verifyLocalModel(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("verify downloaded Laya model: %w", err)
	}
	return os.Rename(tmp, destination)
}

func verifyLocalModel(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != LocalModelBytes {
		return fmt.Errorf("model size mismatch: got %d want %d", info.Size(), LocalModelBytes)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != LocalModelSHA256 {
		return fmt.Errorf("model SHA-256 mismatch: got %s", got)
	}
	return nil
}

func findLlamaServer() (string, error) {
	if binary, err := exec.LookPath("llama-server"); err == nil {
		return binary, nil
	}
	for _, binary := range []string{"/opt/homebrew/bin/llama-server", "/usr/local/bin/llama-server"} {
		if info, err := os.Stat(binary); err == nil && !info.IsDir() {
			return binary, nil
		}
	}
	return "", errors.New("llama-server not found")
}
