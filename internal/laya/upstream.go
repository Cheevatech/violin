package laya

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	UpstreamVersion    = "0.3.20"
	CheckpointRevision = "55cf4c4ebb4ebe31b2550e8bdf3bd21b99753851"
	ORTVersion         = "1.29.0"
	DefaultBundleURL   = "https://github.com/Cheevatech/violin/releases/download/laya-upstream-v0.3.20"
)

type UpstreamStatus struct {
	Installed          bool            `json:"installed"`
	FallbackInUse      bool            `json:"fallback_in_use"`
	SDKVersion         string          `json:"runtime_version"`
	CheckpointRevision string          `json:"checkpoint_revision"`
	Device             string          `json:"device,omitempty"`
	Runtime            string          `json:"runtime,omitempty"`
	Error              string          `json:"error,omitempty"`
	Checkpoints        map[string]bool `json:"checkpoints"`
}

type bundleArtifact struct {
	Path   string `json:"path"`
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type bundleManifest struct {
	FormatVersion      int              `json:"format_version"`
	LayaVersion        string           `json:"laya_version"`
	CheckpointRevision string           `json:"checkpoint_revision"`
	ORTVersion         string           `json:"onnxruntime_version"`
	GOOS               string           `json:"goos"`
	GOARCH             string           `json:"goarch"`
	Artifacts          []bundleArtifact `json:"artifacts"`
}

type InferenceRuntime interface {
	Predict(state string, questions []Question) ([]Answer, string, error)
	Close() error
}

type RuntimeLoader func(root, variant string, preferCoreML bool) (InferenceRuntime, string, error)

// UpstreamEngine keeps one checkpoint resident. A language switch closes the old
// ONNX session before opening the next checkpoint, which bounds its model memory.
type UpstreamEngine struct {
	root    string
	loader  RuntimeLoader
	mu      sync.Mutex
	model   InferenceRuntime
	variant string
	device  string
	closed  bool
}

func StartUpstream(ctx context.Context, root string) (*UpstreamEngine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == "" {
		return nil, errors.New("Laya state directory is required")
	}
	return &UpstreamEngine{root: root, loader: openNativeRuntime}, nil
}

func newUpstreamEngine(root string, loader RuntimeLoader) *UpstreamEngine {
	return &UpstreamEngine{root: root, loader: loader}
}

func (e *UpstreamEngine) Evaluate(request Request) (Result, error) {
	started := time.Now()
	if len(request.Questions) == 0 {
		return Result{ModelVersion: e.modelVersion(""), LatencyMS: 0}, nil
	}
	state, err := serializeUpstreamState(request.State)
	if err != nil {
		return Result{}, err
	}
	variant := SelectCheckpoint(state, request.Language)

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return Result{}, errors.New("upstream Laya runtime is closed")
	}
	if e.model == nil || e.variant != variant {
		if e.model != nil {
			_ = e.model.Close()
			e.model = nil
		}
		if e.loader == nil {
			return Result{}, ErrUnavailable
		}
		preferCoreML := runtime.GOOS == "darwin" && strings.EqualFold(strings.TrimSpace(os.Getenv("VIOLIN_LAYA_COREML")), "true")
		model, device, loadErr := e.loader(e.root, variant, preferCoreML)
		if loadErr != nil {
			return Result{}, fmt.Errorf("load upstream Laya %s checkpoint: %w", variant, loadErr)
		}
		e.model, e.variant, e.device = model, variant, device
	}
	answers, _, err := e.model.Predict(state, request.Questions)
	if err != nil {
		return Result{}, fmt.Errorf("run upstream Laya inference: %w", err)
	}
	result := Result{Answers: answers, ModelVersion: e.modelVersion(variant), LatencyMS: float64(time.Since(started).Microseconds()) / 1000}
	decision, err := decisionFromAnswers(request.Questions, answers, result.ModelVersion)
	if err != nil {
		return Result{}, err
	}
	result.Decision = decision
	return result, nil
}

func (e *UpstreamEngine) modelVersion(variant string) string {
	if variant == "" {
		return "laya-go-onnx@" + CheckpointRevision
	}
	return "laya-go-onnx/" + variant + "@" + CheckpointRevision
}

func (e *UpstreamEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	if e.model == nil {
		return nil
	}
	err := e.model.Close()
	e.model = nil
	return err
}

func decisionFromAnswers(questions []Question, answers []Answer, modelVersion string) (*Decision, error) {
	byID := make(map[string]Answer, len(answers))
	for _, answer := range answers {
		if _, exists := byID[answer.ID]; exists {
			return nil, fmt.Errorf("upstream Laya returned duplicate %q answer", answer.ID)
		}
		byID[answer.ID] = answer
	}
	questionByID := make(map[string]Question, len(questions))
	for _, question := range questions {
		questionByID[question.ID] = question
	}
	choice := func(id string) (string, map[string]float64, error) {
		answer, ok := byID[id]
		if !ok {
			return "", nil, fmt.Errorf("upstream Laya omitted %q answer", id)
		}
		value, ok := answer.Value.(string)
		if !ok || value == "" {
			return "", nil, fmt.Errorf("upstream Laya returned invalid %q answer", id)
		}
		question, ok := questionByID[id]
		if !ok || !containsString(question.Options, value) {
			return "", nil, fmt.Errorf("upstream Laya returned unsupported %q answer %q", id, value)
		}
		if err := validateAnswerProbabilities(id, question.Options, answer.Probabilities, value); err != nil {
			return "", nil, err
		}
		return value, answer.Probabilities, nil
	}
	backend, backendP, err := choice("backend")
	if err != nil {
		return nil, err
	}
	mode, _, err := choice("task_mode")
	if err != nil {
		return nil, err
	}
	risk, _, err := choice("risk")
	if err != nil {
		return nil, err
	}
	timeoutClass, _, err := choice("timeout_policy")
	if err != nil {
		return nil, err
	}
	retryPolicy, _, err := choice("retry_policy")
	if err != nil {
		return nil, err
	}

	ordered := sortedProbabilities(backendP)
	if len(ordered) == 0 || ordered[0].label != backend {
		return nil, errors.New("upstream Laya backend answer does not match its probability distribution")
	}
	confidence := probabilityConfidence(backendP)
	margin := ordered[0].probability
	if len(ordered) > 1 {
		margin -= ordered[1].probability
	}
	headConfidence, headMargin := map[string]float64{}, map[string]float64{}
	for _, id := range []string{"backend", "task_mode", "risk", "timeout_policy", "retry_policy"} {
		answer := byID[id]
		orderedHead := sortedProbabilities(answer.Probabilities)
		headConfidence[id] = probabilityConfidence(answer.Probabilities)
		headMargin[id] = orderedHead[0].probability
		if len(orderedHead) > 1 {
			headMargin[id] -= orderedHead[1].probability
		}
	}
	timeout := map[string]int{"short": 300, "standard": 900, "long": 3600}[timeoutClass]
	if timeout == 0 {
		return nil, fmt.Errorf("unsupported timeout policy %q", timeoutClass)
	}
	maxAttempts := 1
	if retryPolicy == "retry_once" && mode == "inspect" {
		maxAttempts = 2
	}
	d := &Decision{
		BackendCandidates: make([]string, 0, len(ordered)), TaskMode: mode, Risk: Risk(risk),
		TimeoutHintSeconds: timeout, IdleTimeoutEnabled: true,
		Retry: RetryHint{MaxAttempts: maxAttempts}, ExecutionTarget: ExecutionExternal,
		CostTier: TierMedium, LatencyTier: TierMedium, Confidence: confidence, Margin: margin,
		HeadConfidence: headConfidence, HeadMargin: headMargin, ReasonCodes: []string{"upstream_typed_decision"},
		ModelVersion: modelVersion,
	}
	for _, item := range ordered {
		d.BackendCandidates = append(d.BackendCandidates, item.label)
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return d, nil
}

// probabilityConfidence matches upstream laya.rl_common.confidence_from_probs:
// 1 minus normalized entropy. It is derived from the answer distribution and
// never trusts a model- or caller-supplied confidence field.
func probabilityConfidence(probabilities map[string]float64) float64 {
	if len(probabilities) < 2 {
		return 1
	}
	entropy := 0.0
	for _, probability := range probabilities {
		if probability > 0 {
			entropy -= probability * math.Log(probability)
		}
	}
	confidence := 1 - entropy/math.Log(float64(len(probabilities)))
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	return math.Round(confidence*10000) / 10000
}

func validateAnswerProbabilities(id string, options []string, probabilities map[string]float64, chosen string) error {
	if len(probabilities) != len(options) {
		return fmt.Errorf("upstream Laya returned incomplete %q probabilities", id)
	}
	sum, bestValue := 0.0, -1.0
	bestLabel := ""
	for _, option := range options {
		value, ok := probabilities[option]
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("upstream Laya returned invalid %q probability for %q", id, option)
		}
		sum += value
		if value > bestValue {
			bestValue, bestLabel = value, option
		}
	}
	if math.Abs(sum-1) > 0.001 || bestLabel != chosen {
		return fmt.Errorf("upstream Laya %q answer does not match a normalized probability distribution", id)
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type probability struct {
	label       string
	probability float64
}

func sortedProbabilities(values map[string]float64) []probability {
	items := make([]probability, 0, len(values))
	for label, value := range values {
		items = append(items, probability{label, value})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].probability == items[j].probability {
			return items[i].label < items[j].label
		}
		return items[i].probability > items[j].probability
	})
	return items
}

func serializeUpstreamState(state any) (string, error) {
	if text, ok := state.(string); ok {
		return text, nil
	}
	var out strings.Builder
	out.WriteString("{")
	first := true
	var fields []string
	switch value := state.(type) {
	case map[string]any:
		// Keep Violin's routing state in the same task-then-mode order used by
		// its typed request struct; sort any extra caller-provided keys after it.
		for _, key := range []string{"task", "mode"} {
			if _, ok := value[key]; ok {
				fields = append(fields, key)
			}
		}
		priorityCount := len(fields)
		for key := range value {
			if key != "task" && key != "mode" {
				fields = append(fields, key)
			}
		}
		sort.Strings(fields[priorityCount:])
		for _, key := range fields {
			if !first {
				out.WriteByte(',')
			}
			first = false
			keyJSON, _ := marshalCompact(key)
			valueJSON, err := marshalCompact(value[key])
			if err != nil {
				return "", err
			}
			out.Write(keyJSON)
			out.WriteByte(':')
			out.Write(valueJSON)
		}
		out.WriteString("}")
		return out.String(), nil
	default:
		data, err := marshalCompact(state)
		return string(data), err
	}
}

func marshalCompact(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func SelectCheckpoint(state, language string) string {
	if strings.TrimSpace(language) != "" {
		code := strings.ToLower(strings.TrimSpace(language))
		if code != "en" && !strings.HasPrefix(code, "en-") && !strings.HasPrefix(code, "en_") {
			return "multilingual"
		}
	}
	latin, other, accented := 0, 0, false
	for _, r := range state {
		if !isLetter(r) {
			continue
		}
		if isLatin(r) {
			latin++
			if r > 0x007F {
				accented = true
			}
		} else {
			other++
		}
	}
	if accented || (other > 0 && (latin == 0 || float64(other)/float64(latin+other) >= 0.12)) || likelyNonEnglishLatin(state) {
		return "multilingual"
	}
	return "typed-decisions"
}

func likelyNonEnglishLatin(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !((r >= 'a' && r <= 'z') || r == '\'') })
	foreign := map[string]bool{"bonjour": true, "merci": true, "avec": true, "pour": true, "vous": true, "est": true, "une": true, "des": true, "que": true, "je": true, "dans": true, "pas": true, "nous": true, "suis": true, "gracias": true, "por": true, "favor": true, "hola": true, "como": true, "esta": true, "para": true, "una": true, "los": true, "las": true, "pero": true, "porque": true, "necesito": true, "ayuda": true, "con": true, "guten": true, "bitte": true, "nicht": true, "und": true, "ist": true, "fur": true, "ich": true, "das": true, "der": true, "die": true, "mit": true, "brauche": true, "hilfe": true, "obrigado": true, "nao": true, "uma": true, "isso": true, "sono": true, "ciao": true, "perche": true}
	english := map[string]bool{"the": true, "and": true, "please": true, "this": true, "that": true, "with": true, "from": true, "have": true, "would": true, "could": true, "help": true, "need": true, "you": true, "for": true, "is": true, "are": true}
	f, e := 0, 0
	for _, word := range words {
		if foreign[word] {
			f++
		}
		if english[word] {
			e++
		}
	}
	return f >= 2 && f > e
}

func isLetter(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= 0x00C0 && r <= 0x02AF) || (r >= 0x0370 && r <= 0x1FFF) || (r >= 0x2C00 && r <= 0xD7FF) || (r >= 0xF900 && r <= 0xFAFF)
}
func isLatin(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= 0x00C0 && r <= 0x024F)
}

func EnsureUpstream(ctx context.Context, root string) error {
	if _, ok := supportedPlatform(); !ok {
		return fmt.Errorf("upstream Laya ONNX bundles do not support %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	baseURL := strings.TrimRight(os.Getenv("VIOLIN_LAYA_BUNDLE_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = DefaultBundleURL
	}
	platform, _ := supportedPlatform()
	manifestURL := baseURL + "/manifest-" + platform + ".json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch pinned Laya bundle manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch pinned Laya bundle manifest: HTTP %d (%s)", resp.StatusCode, manifestURL)
	}
	var manifest bundleManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&manifest); err != nil {
		return fmt.Errorf("decode Laya bundle manifest: %w", err)
	}
	if err := validateBundleManifest(manifest, platform); err != nil {
		return err
	}
	base := filepath.Join(root, "laya")
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(base, ".bundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, artifact := range manifest.Artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/"+artifact.Asset, nil)
		if err != nil {
			return err
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return fmt.Errorf("download Laya artifact %s: %w", artifact.Asset, err)
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return fmt.Errorf("download Laya artifact %s: HTTP %d", artifact.Asset, response.StatusCode)
		}
		path := filepath.Join(tmp, artifact.Path)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err == nil {
			var file *os.File
			file, err = os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if err == nil {
				h := sha256.New()
				var written int64
				written, err = io.Copy(io.MultiWriter(file, h), io.LimitReader(response.Body, artifact.Size+1))
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
				if err == nil && (written != artifact.Size || hex.EncodeToString(h.Sum(nil)) != strings.ToLower(artifact.SHA256)) {
					err = fmt.Errorf("SHA-256 or size mismatch for %s", artifact.Path)
				}
			}
		}
		response.Body.Close()
		if err != nil {
			return fmt.Errorf("verify Laya artifact %s: %w", artifact.Path, err)
		}
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(tmp, "manifest.json"), manifestData, 0600); err != nil {
		return err
	}
	destination := filepath.Join(base, "onnx")
	old := destination + ".old"
	_ = os.RemoveAll(old)
	if _, err = os.Stat(destination); err == nil {
		if err = os.Rename(destination, old); err != nil {
			return err
		}
	}
	if err = os.Rename(tmp, destination); err != nil {
		_ = os.Rename(old, destination)
		return err
	}
	status := UpstreamRuntimeStatus(root)
	if !status.Installed {
		_ = os.RemoveAll(destination)
		_ = os.Rename(old, destination)
		return fmt.Errorf("installed Laya bundle failed local verification: %s", status.Error)
	}
	_ = os.RemoveAll(old)
	// Remove only Violin's retired Qwen model payload after both pinned upstream
	// checkpoints and the ONNX Runtime library have been verified.
	_ = os.RemoveAll(filepath.Join(base, "models", "qwen3-4b-q4km-2f3b082"))
	_ = os.RemoveAll(filepath.Join(base, "hf-cache", "hub", "models--ggml-org--Qwen3-4B-GGUF"))
	return nil
}

func validateBundleManifest(m bundleManifest, platform string) error {
	if m.FormatVersion != 1 || m.LayaVersion != UpstreamVersion || m.CheckpointRevision != CheckpointRevision || m.ORTVersion != ORTVersion || m.GOOS+"-"+m.GOARCH != platform {
		return errors.New("Laya bundle manifest version, checkpoint, runtime, or platform mismatch")
	}
	required := map[string]bool{}
	if strings.HasPrefix(platform, "darwin-") {
		required["runtime/libonnxruntime.dylib"] = true
	} else {
		required["runtime/libonnxruntime.so"] = true
	}
	for _, variant := range []string{"typed-decisions", "multilingual"} {
		prefix := "checkpoints/" + variant + "/"
		for _, name := range []string{"laya.onnx", "rl_agent_config.json", "tokenizer/tokenizer.json", "tokenizer/tokenizer_config.json"} {
			required[prefix+name] = true
		}
	}
	seen := map[string]bool{}
	seenAssets := map[string]bool{}
	for _, a := range m.Artifacts {
		if filepath.IsAbs(a.Path) || filepath.Clean(a.Path) != a.Path || a.Path == "." || strings.HasPrefix(a.Path, "..") || strings.ContainsAny(a.Path, `\\:`) ||
			a.Asset == "" || len(a.Asset) > 255 || strings.ContainsAny(a.Asset, `/\\?#:`) || a.Size < 1 || a.Size > maxArtifactSize(a.Path) || len(a.SHA256) != 64 {
			return fmt.Errorf("invalid Laya bundle artifact %q", a.Path)
		}
		if seen[a.Path] {
			return fmt.Errorf("duplicate Laya bundle artifact %q", a.Path)
		}
		seen[a.Path] = true
		if seenAssets[a.Asset] {
			return fmt.Errorf("duplicate Laya bundle asset %q", a.Asset)
		}
		seenAssets[a.Asset] = true
		if _, err := hex.DecodeString(a.SHA256); err != nil {
			return fmt.Errorf("invalid SHA-256 for Laya artifact %q", a.Path)
		}
	}
	for path := range required {
		if !seen[path] {
			return fmt.Errorf("Laya bundle manifest is missing required artifact %q", path)
		}
	}
	return nil
}

func maxArtifactSize(path string) int64 {
	switch {
	case strings.HasSuffix(path, ".onnx"):
		return 1 << 30
	case strings.HasSuffix(path, "tokenizer.json"):
		return 64 << 20
	case strings.HasPrefix(path, "runtime/"):
		return 128 << 20
	default:
		return 1 << 20
	}
}

// PackageUpstreamBundle turns a staged, locally exported checkpoint set and ORT
// library into flat release assets plus a SHA-256 manifest. It never downloads
// or executes model conversion code.
func PackageUpstreamBundle(root, output, goos, goarch string) error {
	platform, ok := supportedPlatformFor(goos, goarch)
	if !ok {
		return fmt.Errorf("unsupported bundle platform %s/%s", goos, goarch)
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		return err
	}
	paths := []string{}
	if goos == "darwin" {
		paths = append(paths, "runtime/libonnxruntime.dylib")
	} else {
		paths = append(paths, "runtime/libonnxruntime.so")
	}
	for _, variant := range []string{"typed-decisions", "multilingual"} {
		prefix := "checkpoints/" + variant + "/"
		for _, name := range []string{"laya.onnx", "rl_agent_config.json", "tokenizer/tokenizer.json", "tokenizer/tokenizer_config.json"} {
			paths = append(paths, prefix+name)
		}
	}
	manifest := bundleManifest{FormatVersion: 1, LayaVersion: UpstreamVersion, CheckpointRevision: CheckpointRevision, ORTVersion: ORTVersion, GOOS: goos, GOARCH: goarch}
	for _, relative := range paths {
		source := filepath.Join(root, relative)
		input, err := os.Open(source)
		if err != nil {
			return fmt.Errorf("open staged bundle artifact %s: %w", relative, err)
		}
		asset := "laya-" + strings.TrimPrefix(strings.ReplaceAll(relative, "/", "-"), "checkpoints-")
		if strings.HasPrefix(relative, "runtime/") {
			asset = "onnxruntime-" + platform + "-" + filepath.Base(relative)
		}
		target := filepath.Join(output, asset)
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			return err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(out, hash), input)
		inputErr, outputErr := input.Close(), out.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		if outputErr != nil {
			return outputErr
		}
		if size == 0 {
			return fmt.Errorf("staged bundle artifact %s is empty", relative)
		}
		manifest.Artifacts = append(manifest.Artifacts, bundleArtifact{Path: relative, Asset: asset, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size})
	}
	if err := validateBundleManifest(manifest, platform); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, "manifest-"+platform+".json"), data, 0600)
}

func supportedPlatform() (string, bool) {
	return supportedPlatformFor(runtime.GOOS, runtime.GOARCH)
}

func supportedPlatformFor(goos, goarch string) (string, bool) {
	if goos == "darwin" && goarch != "arm64" {
		return "", false
	}
	if goos == "linux" && goarch != "arm64" && goarch != "amd64" {
		return "", false
	}
	if goos != "darwin" && goos != "linux" {
		return "", false
	}
	return goos + "-" + goarch, true
}

func UpstreamRuntimeStatus(root string) UpstreamStatus {
	s := UpstreamStatus{SDKVersion: "onnxruntime-go/" + ORTVersion, CheckpointRevision: CheckpointRevision, Device: "cpu (default)", FallbackInUse: true, Checkpoints: map[string]bool{"typed-decisions": false, "multilingual": false}}
	platform, ok := supportedPlatform()
	if !ok {
		s.Error = fmt.Sprintf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
		return s
	}
	bundleDir := filepath.Join(root, "laya", "onnx")
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		s.Error = "pinned upstream Laya ONNX bundles are not installed"
		return s
	}
	var manifest bundleManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		s.Error = "invalid local Laya bundle manifest"
		return s
	}
	if err = validateBundleManifest(manifest, platform); err != nil {
		s.Error = err.Error()
		return s
	}
	for _, artifact := range manifest.Artifacts {
		path := filepath.Join(bundleDir, artifact.Path)
		if err = verifyBundleFile(path, artifact); err != nil {
			s.Error = err.Error()
			return s
		}
		if strings.HasPrefix(artifact.Path, "checkpoints/typed-decisions/") {
			s.Checkpoints["typed-decisions"] = true
		}
		if strings.HasPrefix(artifact.Path, "checkpoints/multilingual/") {
			s.Checkpoints["multilingual"] = true
		}
		if strings.HasPrefix(artifact.Path, "runtime/") {
			s.Runtime = path
		}
	}
	if !s.Checkpoints["typed-decisions"] || !s.Checkpoints["multilingual"] || s.Runtime == "" {
		s.Error = "Laya bundle is incomplete"
		return s
	}
	if !nativeRuntimeAvailable() {
		s.Error = "this Violin build has CGO disabled; upstream Laya inference is unavailable"
		return s
	}
	s.Installed, s.FallbackInUse = true, false
	if runtime.GOOS == "darwin" {
		s.Device = "cpu (default; set VIOLIN_LAYA_COREML=true to try experimental CoreML)"
	}
	return s
}

func verifyBundleFile(path string, artifact bundleArtifact) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("missing Laya artifact %s", artifact.Path)
	}
	defer file.Close()
	h := sha256.New()
	size, err := io.Copy(h, bufio.NewReader(file))
	if err != nil {
		return fmt.Errorf("read Laya artifact %s: %w", artifact.Path, err)
	}
	if size != artifact.Size || hex.EncodeToString(h.Sum(nil)) != strings.ToLower(artifact.SHA256) {
		return fmt.Errorf("Laya artifact integrity check failed: %s", artifact.Path)
	}
	return nil
}
