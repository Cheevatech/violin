package laya

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/film/violin/internal/models"
)

const DefaultModelVersion = "builtin-2"

func DefaultModel() Model {
	return Model{
		SchemaVersion: 1,
		ID:            models.DefaultModelID,
		Version:       DefaultModelVersion,
		Language:      ProtocolLanguage,
		Heads: map[string]Head{
			"backend": {
				Labels:  []string{"qwen", "agy", "claude"},
				Bias:    []float64{0, 0, 0},
				Weights: map[string][]float64{"implement": {3, 0, 0}, "research": {0, 3, 0}, "review": {0, 0, 3}, "reason": {3, 0, 0}},
			},
			"task_mode": {
				Labels:  []string{"inspect", "implement"},
				Bias:    []float64{0, 0},
				Weights: map[string][]float64{"implement": {0, 3}, "fix": {0, 3}, "change": {0, 2}, "review": {2, 0}},
			},
			"risk": {
				Labels:  []string{"low", "medium", "high"},
				Bias:    []float64{1, 0, 0},
				Weights: map[string][]float64{"security": {0, 0, 3}, "credential": {0, 0, 3}, "secret": {0, 0, 3}, "delete": {0, 2, 1}},
			},
		},
	}
}

func EnsureDefaultModel(root string) error {
	manager, err := models.NewManager(filepath.Join(root, "models"))
	if err != nil {
		return err
	}
	status := manager.Status()
	if status.Active == DefaultModelVersion && status.Verified {
		return nil
	}
	data, err := json.Marshal(DefaultModel())
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(root, ".laya-default-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.json"), data, 0600); err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	return manager.Install(models.Manifest{ID: models.DefaultModelID, Language: models.DefaultLanguage, Version: DefaultModelVersion, Runtime: "builtin-go", Artifacts: []models.Artifact{{Path: "model.json", SHA256: hex.EncodeToString(hash[:]), Size: int64(len(data))}}}, tmp, true)
}
