//go:build cgo

package laya

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
	ort "github.com/yalue/onnxruntime_go"
)

var ortInit sync.Mutex

func nativeRuntimeAvailable() bool { return true }

type nativeConfig struct {
	MaxLen               int                `json:"max_len"`
	HeadMaxLen           int                `json:"head_max_len"`
	Temperature          []float64          `json:"temperature"`
	TemperatureByOptions map[string]float64 `json:"temperature_by_options"`
}

type nativeTokenizerConfig struct {
	MaskToken json.RawMessage `json:"mask_token"`
	CLSToken  json.RawMessage `json:"cls_token"`
	SepToken  json.RawMessage `json:"sep_token"`
	PadToken  json.RawMessage `json:"pad_token"`
}

type nativeModel struct {
	session                     *ort.DynamicAdvancedSession
	tokenizer                   *tokenizer.Tokenizer
	config                      nativeConfig
	maskID, clsID, sepID, padID int
	device                      string
	mu                          sync.Mutex
}

func openNativeRuntime(root, variant string, preferCoreML bool) (InferenceRuntime, string, error) {
	base := filepath.Join(root, "laya", "onnx", "checkpoints", variant)
	status := UpstreamRuntimeStatus(root)
	if !status.Installed {
		return nil, "", fmt.Errorf("verified ONNX bundle is unavailable: %s", status.Error)
	}
	if err := initializeORT(status.Runtime); err != nil {
		return nil, "", err
	}
	var tokenizerConfig nativeTokenizerConfig
	configPath := filepath.Join(base, "tokenizer", "tokenizer_config.json")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "", err
	}
	if err = json.Unmarshal(configBytes, &tokenizerConfig); err != nil {
		return nil, "", fmt.Errorf("decode tokenizer configuration: %w", err)
	}
	maskToken, err := tokenContent(tokenizerConfig.MaskToken)
	if err != nil {
		return nil, "", err
	}
	clsToken, err := tokenContent(tokenizerConfig.CLSToken)
	if err != nil {
		return nil, "", err
	}
	sepToken, err := tokenContent(tokenizerConfig.SepToken)
	if err != nil {
		return nil, "", err
	}
	padToken, err := tokenContent(tokenizerConfig.PadToken)
	if err != nil {
		return nil, "", err
	}
	tk, err := pretrained.FromFile(filepath.Join(base, "tokenizer", "tokenizer.json"))
	if err != nil {
		return nil, "", fmt.Errorf("load pinned Laya tokenizer: %w", err)
	}
	maskID, ok := tk.TokenToId(maskToken)
	if !ok {
		return nil, "", fmt.Errorf("Laya tokenizer has no mask token %q", maskToken)
	}
	clsID, ok := tk.TokenToId(clsToken)
	if !ok {
		return nil, "", fmt.Errorf("Laya tokenizer has no CLS token %q", clsToken)
	}
	sepID, ok := tk.TokenToId(sepToken)
	if !ok {
		return nil, "", fmt.Errorf("Laya tokenizer has no SEP token %q", sepToken)
	}
	padID, ok := tk.TokenToId(padToken)
	if !ok {
		return nil, "", fmt.Errorf("Laya tokenizer has no pad token %q", padToken)
	}
	var cfg nativeConfig
	data, err := os.ReadFile(filepath.Join(base, "rl_agent_config.json"))
	if err != nil {
		return nil, "", err
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		return nil, "", fmt.Errorf("decode Laya model configuration: %w", err)
	}
	if cfg.MaxLen < 8 || cfg.HeadMaxLen < 16 {
		return nil, "", errors.New("Laya model sequence limits are invalid")
	}
	if len(cfg.Temperature) != 3 {
		cfg.Temperature = []float64{1, 1, 1}
	}
	if cfg.TemperatureByOptions == nil {
		cfg.TemperatureByOptions = map[string]float64{}
	}
	modelPath := filepath.Join(base, "laya.onnx")
	session, device, err := openORTSession(modelPath, preferCoreML)
	if err != nil {
		return nil, "", err
	}
	return &nativeModel{session: session, tokenizer: tk, config: cfg, maskID: maskID, clsID: clsID, sepID: sepID, padID: padID, device: device}, device, nil
}

func initializeORT(libraryPath string) error {
	ortInit.Lock()
	defer ortInit.Unlock()
	if ort.IsInitialized() {
		return nil
	}
	if path := os.Getenv("VIOLIN_ONNXRUNTIME_PATH"); path != "" {
		libraryPath = path
	}
	if libraryPath == "" {
		return errors.New("ONNX Runtime shared library is not installed")
	}
	ort.SetSharedLibraryPath(libraryPath)
	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("initialize ONNX Runtime %s: %w", libraryPath, err)
	}
	return nil
}

func openORTSession(modelPath string, preferCoreML bool) (*ort.DynamicAdvancedSession, string, error) {
	if preferCoreML && runtime.GOOS == "darwin" {
		options, err := ort.NewSessionOptions()
		if err == nil {
			err = options.AppendExecutionProviderCoreML(0)
			if err == nil {
				session, openErr := ort.NewDynamicAdvancedSession(modelPath,
					[]string{"input_ids", "attention_mask", "marker_pos", "marker_mask", "qtype"},
					[]string{"logits", "act_logits"}, options)
				_ = options.Destroy()
				if openErr == nil {
					return session, "coreml-experimental", nil
				}
			} else {
				_ = options.Destroy()
			}
		}
	}
	session, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask", "marker_pos", "marker_mask", "qtype"},
		[]string{"logits", "act_logits"}, nil)
	if err != nil {
		return nil, "", fmt.Errorf("open Laya ONNX session on CPU: %w", err)
	}
	return session, "cpu", nil
}

func tokenContent(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var item struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil || item.Content == "" {
		return "", errors.New("Laya tokenizer special token is missing or invalid")
	}
	return item.Content, nil
}

type sequenceItem struct {
	ids     []int
	markers []int
}

func (m *nativeModel) Predict(state string, questions []Question) ([]Answer, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(questions) == 0 {
		return nil, m.device, nil
	}
	state = strings.ReplaceAll(state, m.maskToken(), " ")
	stateIDs, err := m.encode(state)
	if err != nil {
		return nil, m.device, err
	}
	items := make([]sequenceItem, len(questions))
	maxOptions, maxLen := 0, 0
	for index, q := range questions {
		if q.Kind != Choice || len(q.Options) < 2 {
			return nil, m.device, fmt.Errorf("Laya ONNX currently requires choice questions with at least two options (%s)", q.ID)
		}
		item, buildErr := m.buildSequence(stateIDs, q)
		if buildErr != nil {
			return nil, m.device, buildErr
		}
		items[index] = item
		if len(item.markers) > maxOptions {
			maxOptions = len(item.markers)
		}
		if len(item.ids) > maxLen {
			maxLen = len(item.ids)
		}
	}
	if maxOptions < 2 {
		maxOptions = 2
	}
	if maxLen < 8 {
		maxLen = 8
	}
	if maxOptions == 0 || maxLen == 0 {
		return nil, m.device, errors.New("Laya generated an empty model input")
	}
	rows := len(items)
	inputIDs := make([]int64, rows*maxLen)
	attention := make([]int64, rows*maxLen)
	markerPos := make([]int64, rows*maxOptions)
	markerMask := make([]bool, rows*maxOptions)
	qtypes := make([]int64, rows)
	for row, item := range items {
		for col := 0; col < maxLen; col++ {
			inputIDs[row*maxLen+col] = int64(m.padID)
		}
		for col, id := range item.ids {
			inputIDs[row*maxLen+col] = int64(id)
			attention[row*maxLen+col] = 1
		}
		for col, pos := range item.markers {
			markerPos[row*maxOptions+col] = int64(pos)
			markerMask[row*maxOptions+col] = true
		}
		qtypes[row] = int64(questionTypeID(questions[row].Kind))
	}
	idsTensor, err := ort.NewTensor(ort.NewShape(int64(rows), int64(maxLen)), inputIDs)
	if err != nil {
		return nil, m.device, err
	}
	defer idsTensor.Destroy()
	attentionTensor, err := ort.NewTensor(ort.NewShape(int64(rows), int64(maxLen)), attention)
	if err != nil {
		return nil, m.device, err
	}
	defer attentionTensor.Destroy()
	markerPosTensor, err := ort.NewTensor(ort.NewShape(int64(rows), int64(maxOptions)), markerPos)
	if err != nil {
		return nil, m.device, err
	}
	defer markerPosTensor.Destroy()
	markerMaskTensor, err := ort.NewTensor(ort.NewShape(int64(rows), int64(maxOptions)), markerMask)
	if err != nil {
		return nil, m.device, err
	}
	defer markerMaskTensor.Destroy()
	qtypeTensor, err := ort.NewTensor(ort.NewShape(int64(rows)), qtypes)
	if err != nil {
		return nil, m.device, err
	}
	defer qtypeTensor.Destroy()
	inputs := []ort.Value{idsTensor, attentionTensor, markerPosTensor, markerMaskTensor, qtypeTensor}
	outputs := []ort.Value{nil, nil}
	if err := m.session.Run(inputs, outputs); err != nil {
		return nil, m.device, fmt.Errorf("ONNX Runtime inference: %w", err)
	}
	defer outputs[0].Destroy()
	defer outputs[1].Destroy()
	logits, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, m.device, errors.New("Laya ONNX logits output is not float32")
	}
	logitData := logits.GetData()
	if len(logitData) < rows*maxOptions {
		return nil, m.device, errors.New("Laya ONNX logits output has an unexpected shape")
	}
	answers := make([]Answer, len(questions))
	for row, q := range questions {
		k := len(items[row].markers)
		temperature := m.temperature(k)
		maxLogit := float64(logitData[row*maxOptions]) / temperature
		for col := 1; col < k; col++ {
			value := float64(logitData[row*maxOptions+col]) / temperature
			if value > maxLogit {
				maxLogit = value
			}
		}
		exp := make([]float64, k)
		total := 0.0
		for col := 0; col < k; col++ {
			exp[col] = math.Exp(float64(logitData[row*maxOptions+col])/temperature - maxLogit)
			total += exp[col]
		}
		probabilities := make(map[string]float64, k)
		best := 0
		for col, value := range exp {
			p := math.Round(value/total*10000) / 10000
			probabilities[q.Options[col]] = p
			if value > exp[best] {
				best = col
			}
		}
		confidence := probabilityConfidence(probabilities)
		answers[row] = Answer{ID: q.ID, Kind: q.Kind, Value: q.Options[best], Probabilities: probabilities, Confidence: confidence}
	}
	return answers, m.device, nil
}

func questionTypeID(kind Kind) int {
	switch kind {
	case Choice:
		return 0
	case Score:
		return 1
	case Noul:
		return 2
	default:
		return -1
	}
}

func (m *nativeModel) maskToken() string { token, _ := m.tokenizer.IdToToken(m.maskID); return token }

func (m *nativeModel) encode(text string) ([]int, error) {
	encoding, err := m.tokenizer.EncodeSingle(text, false)
	if err != nil {
		return nil, err
	}
	return encoding.GetIds(), nil
}

func (m *nativeModel) buildSequence(stateIDs []int, q Question) (sequenceItem, error) {
	mask := m.maskToken()
	instruction := strings.ReplaceAll(q.Prompt, mask, " ")
	head, err := m.encode(fmt.Sprintf("%s question: %s", q.Kind, instruction))
	if err != nil {
		return sequenceItem{}, err
	}
	options := make([][]int, len(q.Options))
	optionTokenCount := 0
	for i, option := range q.Options {
		encoded, encodeErr := m.encode(" " + strings.ReplaceAll(option, mask, " "))
		if encodeErr != nil {
			return sequenceItem{}, encodeErr
		}
		if len(encoded) > 48 {
			encoded = encoded[:48]
		}
		options[i] = append([]int{m.maskID}, encoded...)
		optionTokenCount += len(options[i])
	}
	budget := m.config.HeadMaxLen - optionTokenCount
	if budget < 16 {
		per := (m.config.HeadMaxLen - 16) / max(1, len(options))
		if per < 4 {
			per = 4
		}
		optionTokenCount = 0
		for i := range options {
			if len(options[i]) > per {
				options[i] = options[i][:per]
			}
			optionTokenCount += len(options[i])
		}
		budget = m.config.HeadMaxLen - optionTokenCount
	}
	headLimit := max(8, budget)
	if len(head) > headLimit {
		head = head[:headLimit]
	}
	ids := make([]int, 0, min(m.config.MaxLen, len(head)+optionTokenCount+len(stateIDs)+3))
	ids = append(ids, m.clsID)
	ids = append(ids, head...)
	ids = append(ids, m.sepID)
	markers := make([]int, 0, len(options))
	for _, option := range options {
		markers = append(markers, len(ids))
		ids = append(ids, option...)
	}
	ids = append(ids, m.sepID)
	room := max(0, m.config.MaxLen-len(ids)-1)
	stateCount := min(len(stateIDs), room)
	ids = append(ids, stateIDs[:stateCount]...)
	ids = append(ids, m.sepID)
	if len(ids) > m.config.MaxLen {
		ids = ids[:m.config.MaxLen]
	}
	validMarkers := markers[:0]
	for _, pos := range markers {
		if pos < len(ids) {
			validMarkers = append(validMarkers, pos)
		}
	}
	if len(validMarkers) != len(options) {
		return sequenceItem{}, errors.New("Laya options exceeded the checkpoint head token limit")
	}
	return sequenceItem{ids: ids, markers: validMarkers}, nil
}

func (m *nativeModel) temperature(optionCount int) float64 {
	bucket := "choice:2"
	if optionCount > 10 {
		bucket = "choice:11+"
	} else if optionCount > 5 {
		bucket = "choice:6-10"
	} else if optionCount > 2 {
		bucket = "choice:3-5"
	}
	if value, ok := m.config.TemperatureByOptions[bucket]; ok && value >= 0.5 && value <= 5 {
		return value
	}
	if len(m.config.Temperature) > 0 && m.config.Temperature[0] >= 0.5 && m.config.Temperature[0] <= 5 {
		return m.config.Temperature[0]
	}
	return 1
}

func (m *nativeModel) Close() error {
	if m.session == nil {
		return nil
	}
	err := m.session.Destroy()
	m.session = nil
	return err
}
