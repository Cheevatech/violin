package laya

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

type LabeledExample struct {
	Task          string `json:"task"`
	Language      string `json:"language"`
	Backend       string `json:"backend"`
	Mode          string `json:"task_mode"`
	Risk          Risk   `json:"risk"`
	TimeoutPolicy string `json:"timeout_policy"`
	RetryPolicy   string `json:"retry_policy"`
	Split         string `json:"split"`
	Reviewed      bool   `json:"reviewed"`
}

type HeadMetrics struct {
	Examples                int                `json:"examples"`
	PerClassSupport         map[string]int     `json:"per_class_support"`
	PerClassPrecision       map[string]float64 `json:"per_class_precision"`
	PerClassAutoPrecision   map[string]float64 `json:"per_class_auto_precision"`
	PerClassAutoPredictions map[string]int     `json:"per_class_auto_predictions"`
	AutoCoverage            float64            `json:"auto_coverage"`
	AutoPrecision           float64            `json:"auto_precision"`
	ECE                     float64            `json:"ece"`
}

type TrainingReport struct {
	TrainExamples   int                    `json:"train_examples"`
	HoldoutExamples int                    `json:"holdout_examples"`
	Heads           map[string]HeadMetrics `json:"heads"`
	HighRiskRecall  float64                `json:"high_risk_recall"`
	MeetsGate       bool                   `json:"meets_gate"`
	Failures        []string               `json:"failures,omitempty"`
}

func LoadDataset(path string) ([]LabeledExample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var result []LabeledExample
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		var example LabeledExample
		if err := json.Unmarshal(scanner.Bytes(), &example); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		if err := example.Validate(); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		result = append(result, example)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (e LabeledExample) Validate() error {
	if strings.TrimSpace(e.Task) == "" {
		return errors.New("task is required")
	}
	if e.Language != ProtocolLanguage {
		return fmt.Errorf("language must be %q", ProtocolLanguage)
	}
	if !e.Reviewed {
		return errors.New("example must be reviewed before training")
	}
	if e.Backend != "agy" && e.Backend != "qwen" && e.Backend != "claude" {
		return fmt.Errorf("invalid backend %q", e.Backend)
	}
	if e.Mode != "inspect" && e.Mode != "implement" {
		return fmt.Errorf("invalid task_mode %q", e.Mode)
	}
	if e.Risk != RiskLow && e.Risk != RiskMedium && e.Risk != RiskHigh {
		return fmt.Errorf("invalid risk %q", e.Risk)
	}
	if e.TimeoutPolicy != "short" && e.TimeoutPolicy != "standard" && e.TimeoutPolicy != "long" {
		return errors.New("invalid timeout_policy")
	}
	if e.RetryPolicy != "never" && e.RetryPolicy != "inspect_once" {
		return errors.New("invalid retry_policy")
	}
	if e.Split != "train" && e.Split != "holdout" {
		return fmt.Errorf("split must be train or holdout")
	}
	return nil
}

func TrainDataset(examples []LabeledExample, version string) (Model, TrainingReport, error) {
	if strings.TrimSpace(version) == "" {
		return Model{}, TrainingReport{}, errors.New("model version is required")
	}
	for index, example := range examples {
		if err := example.Validate(); err != nil {
			return Model{}, TrainingReport{}, fmt.Errorf("dataset example %d: %w", index+1, err)
		}
	}
	train, holdout := splitDataset(examples)
	if len(train) == 0 || len(holdout) == 0 {
		return Model{}, TrainingReport{}, errors.New("dataset requires train and holdout examples")
	}
	seen := map[string]bool{}
	for _, example := range train {
		seen[strings.ToLower(strings.TrimSpace(example.Task))] = true
	}
	for _, example := range holdout {
		if seen[strings.ToLower(strings.TrimSpace(example.Task))] {
			return Model{}, TrainingReport{}, errors.New("holdout tasks must not duplicate training tasks")
		}
	}
	model := Model{SchemaVersion: 1, ID: "laya-english", Version: version, Language: ProtocolLanguage, Heads: map[string]Head{}}
	labels := map[string][]string{"backend": {"qwen", "agy", "claude"}, "task_mode": {"inspect", "implement"}, "risk": {"low", "medium", "high"}, "timeout_policy": {"short", "standard", "long"}, "retry_policy": {"never", "inspect_once"}}
	for name, values := range labels {
		head, err := fitHead(train, name, values)
		if err != nil {
			return Model{}, TrainingReport{}, err
		}
		model.Heads[name] = head
	}
	if err := model.Validate(); err != nil {
		return Model{}, TrainingReport{}, err
	}
	report := evaluateDataset(model, train, holdout)
	return model, report, nil
}

func splitDataset(all []LabeledExample) ([]LabeledExample, []LabeledExample) {
	var train, holdout []LabeledExample
	for _, e := range all {
		if e.Split == "train" {
			train = append(train, e)
		} else if e.Split == "holdout" {
			holdout = append(holdout, e)
		}
	}
	return train, holdout
}

func fitHead(examples []LabeledExample, name string, labels []string) (Head, error) {
	labelIndex := map[string]int{}
	for i, label := range labels {
		labelIndex[label] = i
	}
	features := map[string]bool{}
	for _, e := range examples {
		for feature := range extractFeatures(e.Task) {
			features[feature] = true
		}
	}
	ordered := make([]string, 0, len(features))
	for feature := range features {
		ordered = append(ordered, feature)
	}
	sort.Strings(ordered)
	counts := make([]int, len(labels))
	for _, e := range examples {
		if _, ok := labelIndex[e.label(name)]; !ok {
			return Head{}, fmt.Errorf("invalid label for %s", name)
		}
		counts[labelIndex[e.label(name)]]++
	}
	for i, count := range counts {
		if count == 0 {
			return Head{}, fmt.Errorf("training data has no %s examples for %q", name, labels[i])
		}
	}
	head := Head{Labels: append([]string(nil), labels...), Bias: make([]float64, len(labels)), Weights: map[string][]float64{}}
	for _, feature := range ordered {
		head.Weights[feature] = make([]float64, len(labels))
	}
	for epoch := 0; epoch < 300; epoch++ {
		lr := .08 / (1 + float64(epoch)*.01)
		for _, example := range examples {
			fs := extractFeatures(example.Task)
			scores := append([]float64(nil), head.Bias...)
			for _, feature := range ordered {
				value, exists := fs[feature]
				if !exists {
					continue
				}
				if weights := head.Weights[feature]; weights != nil {
					for i, w := range weights {
						scores[i] += value * w
					}
				}
			}
			probs := softmax(scores)
			target := labelIndex[example.label(name)]
			for i := range head.Bias {
				gradient := probs[i]
				if i == target {
					gradient--
				}
				head.Bias[i] -= lr * gradient
			}
			for _, feature := range ordered {
				value, exists := fs[feature]
				if !exists {
					continue
				}
				weights := head.Weights[feature]
				if weights == nil {
					continue
				}
				for i := range weights {
					gradient := probs[i]
					if i == target {
						gradient--
					}
					weights[i] -= lr * (gradient*value + .0005*weights[i])
				}
				head.Weights[feature] = weights
			}
		}
	}
	return head, nil
}

func (e LabeledExample) label(head string) string {
	switch head {
	case "backend":
		return e.Backend
	case "task_mode":
		return e.Mode
	case "risk":
		return string(e.Risk)
	case "timeout_policy":
		return e.TimeoutPolicy
	default:
		return e.RetryPolicy
	}
}

func evaluateDataset(model Model, train, holdout []LabeledExample) TrainingReport {
	report := TrainingReport{TrainExamples: len(train), HoldoutExamples: len(holdout), Heads: map[string]HeadMetrics{}}
	labels := map[string][]string{"backend": {"qwen", "agy", "claude"}, "task_mode": {"inspect", "implement"}, "risk": {"low", "medium", "high"}, "timeout_policy": {"short", "standard", "long"}, "retry_policy": {"never", "inspect_once"}}
	highTotal, highCorrect := 0, 0
	for headName, headLabels := range labels {
		metric := HeadMetrics{Examples: len(holdout), PerClassSupport: map[string]int{}, PerClassPrecision: map[string]float64{}, PerClassAutoPrecision: map[string]float64{}, PerClassAutoPredictions: map[string]int{}}
		correct := map[string]int{}
		predicted := map[string]int{}
		autoCorrectByClass := map[string]int{}
		autoCount, autoCorrect := 0, 0
		binCount := make([]int, 10)
		binConfidence := make([]float64, 10)
		binAccuracy := make([]float64, 10)
		for _, e := range holdout {
			gold := e.label(headName)
			metric.PerClassSupport[gold]++
			p := model.predict(headName, extractFeatures(e.Task))
			if p == nil {
				continue
			}
			predicted[p.label]++
			isCorrect := p.label == gold
			if isCorrect {
				correct[p.label]++
			}
			if headName == "risk" && gold == string(RiskHigh) {
				highTotal++
				if p.label == gold {
					highCorrect++
				}
			}
			bin := int(p.confidence * 10)
			if bin > 9 {
				bin = 9
			}
			binCount[bin]++
			binConfidence[bin] += p.confidence
			if isCorrect {
				binAccuracy[bin]++
			}
			if p.confidence >= .8 && p.margin >= .15 {
				autoCount++
				metric.PerClassAutoPredictions[p.label]++
				if isCorrect {
					autoCorrect++
					autoCorrectByClass[p.label]++
				}
			}
		}
		for _, label := range headLabels {
			if predicted[label] == 0 {
				metric.PerClassPrecision[label] = 0
			} else {
				metric.PerClassPrecision[label] = float64(correct[label]) / float64(predicted[label])
			}
			if metric.PerClassSupport[label] < 100 {
				report.Failures = append(report.Failures, headName+" holdout support <100 for "+label)
			}
			if metric.PerClassAutoPredictions[label] > 0 {
				metric.PerClassAutoPrecision[label] = float64(autoCorrectByClass[label]) / float64(metric.PerClassAutoPredictions[label])
				if metric.PerClassAutoPrecision[label] < .95 {
					report.Failures = append(report.Failures, headName+" automatic precision <95% for "+label)
				}
			}
		}
		if autoCount > 0 {
			metric.AutoCoverage = float64(autoCount) / float64(len(holdout))
			metric.AutoPrecision = float64(autoCorrect) / float64(autoCount)
		}
		for i, count := range binCount {
			if count > 0 {
				metric.ECE += float64(count) / float64(len(holdout)) * math.Abs(binAccuracy[i]/float64(count)-binConfidence[i]/float64(count))
			}
		}
		if autoCount == 0 || metric.AutoPrecision < .95 {
			report.Failures = append(report.Failures, headName+" automatic precision <95% or no covered samples")
		}
		if metric.ECE > .10 {
			report.Failures = append(report.Failures, headName+" ECE >0.10")
		}
		report.Heads[headName] = metric
	}
	if highTotal > 0 {
		report.HighRiskRecall = float64(highCorrect) / float64(highTotal)
	}
	if highTotal < 100 || highCorrect != highTotal {
		report.Failures = append(report.Failures, "high-risk recall must be 100% with at least 100 holdout examples")
	}
	report.MeetsGate = len(report.Failures) == 0
	return report
}
