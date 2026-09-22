package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}
type Manifest struct {
	ID        string     `json:"id"`
	Version   string     `json:"version"`
	Source    string     `json:"source"`
	Revision  string     `json:"revision"`
	Runtime   string     `json:"runtime"`
	Artifacts []Artifact `json:"artifacts"`
	CreatedAt time.Time  `json:"created_at"`
}
type Status struct {
	Root     string    `json:"root"`
	Active   string    `json:"active,omitempty"`
	Manifest *Manifest `json:"manifest,omitempty"`
	Verified bool      `json:"verified"`
}

type Manager struct{ root, versions, active string }

func NewManager(root string) (*Manager, error) {
	if root == "" {
		return nil, errors.New("model root is required")
	}
	m := &Manager{root: root, versions: filepath.Join(root, "versions"), active: filepath.Join(root, "active")}
	if err := os.MkdirAll(m.versions, 0700); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) Install(manifest Manifest, sourceDir string, activate bool) error {
	if manifest.ID == "" || manifest.Version == "" || len(manifest.Artifacts) == 0 {
		return errors.New("model manifest requires id, version, and artifacts")
	}
	if sourceDir == "" {
		return errors.New("model source directory is required")
	}
	tmp, err := os.MkdirTemp(m.versions, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for i := range manifest.Artifacts {
		artifact := &manifest.Artifacts[i]
		rel, err := safeRelativePath(artifact.Path)
		if err != nil {
			return err
		}
		src := filepath.Join(sourceDir, rel)
		if err := verifyFile(src, artifact.SHA256); err != nil {
			return fmt.Errorf("verify %s: %w", artifact.Path, err)
		}
		dst := filepath.Join(tmp, rel)
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	manifest.CreatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(tmp, "manifest.json"), data, 0600); err != nil {
		return err
	}
	destination := filepath.Join(m.versions, manifest.Version)
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("model version already installed: %s", manifest.Version)
	}
	if err = os.Rename(tmp, destination); err != nil {
		return err
	}
	if activate {
		return m.activate(manifest.Version)
	}
	return nil
}

func (m *Manager) InstallFromURL(manifest Manifest, baseURL string) error {
	if strings.TrimSpace(baseURL) == "" {
		return errors.New("model base URL is required")
	}
	tmp, err := os.MkdirTemp(m.root, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, artifact := range manifest.Artifacts {
		rel, err := safeRelativePath(artifact.Path)
		if err != nil {
			return err
		}
		response, err := http.Get(strings.TrimRight(baseURL, "/") + "/" + rel)
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return fmt.Errorf("download %s: HTTP %d", rel, response.StatusCode)
		}
		path := filepath.Join(tmp, rel)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err == nil {
			var output *os.File
			output, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if err == nil {
				_, err = io.Copy(output, response.Body)
				_ = output.Close()
			}
		}
		_ = response.Body.Close()
		if err != nil {
			return err
		}
	}
	return m.Install(manifest, tmp, true)
}

func (m *Manager) Verify() error {
	version, err := m.activeVersion()
	if err != nil {
		return err
	}
	manifest, err := m.readManifest(version)
	if err != nil {
		return err
	}
	for _, artifact := range manifest.Artifacts {
		if err := verifyFile(filepath.Join(m.versions, version, artifact.Path), artifact.SHA256); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) Rollback(version string) error {
	if _, err := m.readManifest(version); err != nil {
		return err
	}
	return m.activate(version)
}
func (m *Manager) ActivePath() (string, error) {
	version, err := m.activeVersion()
	if err != nil {
		return "", err
	}
	if _, err = m.readManifest(version); err != nil {
		return "", err
	}
	return filepath.Join(m.versions, version), nil
}
func (m *Manager) Status() Status {
	result := Status{Root: m.root}
	version, err := m.activeVersion()
	if err != nil {
		return result
	}
	result.Active = version
	result.Manifest, _ = m.readManifest(version)
	result.Verified = m.Verify() == nil
	return result
}

func (m *Manager) activate(version string) error {
	tmp := m.active + ".tmp"
	if err := os.WriteFile(tmp, []byte(version+"\n"), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, m.active)
}
func (m *Manager) activeVersion() (string, error) {
	data, err := os.ReadFile(m.active)
	if err != nil {
		return "", err
	}
	value := string(data)
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r') {
		value = value[:len(value)-1]
	}
	if value == "" {
		return "", errors.New("active model is empty")
	}
	return value, nil
}
func (m *Manager) readManifest(version string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(m.versions, version, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func verifyFile(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if expected != "" && actual != expected {
		return fmt.Errorf("sha256 mismatch: got %s want %s", actual, expected)
	}
	if info.Size() < 0 {
		return errors.New("invalid artifact size")
	}
	return nil
}
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
func safeRelativePath(value string) (string, error) {
	clean := filepath.Clean(value)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe model artifact path: %q", value)
	}
	return clean, nil
}
