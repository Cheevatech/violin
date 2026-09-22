package setup

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// bundledSkills is compiled into the release binary so `npx violin` does not
// depend on the repository checkout being the current working directory.
//
//go:embed assets
var bundledSkills embed.FS

const (
	startMarker = "# BEGIN VIOLIN MANAGED MCP"
	endMarker   = "# END VIOLIN MANAGED MCP"
)

type Plan struct {
	Action  string `json:"action"`
	Target  string `json:"target"`
	Apply   bool   `json:"apply"`
	Changes string `json:"changes"`
	Backup  string `json:"backup,omitempty"`
}

func MCPPlan(binary string, apply bool) (Plan, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Plan{}, err
	}
	target := filepath.Join(home, ".codex", "config.toml")
	original, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return Plan{}, err
	}
	updated, err := replaceManagedBlock(string(original), fmt.Sprintf("%s\n[mcp_servers.violin]\ncommand = %q\ntool_timeout_sec = 60\n%s", startMarker, binary, endMarker))
	if err != nil {
		return Plan{}, err
	}
	planning := Plan{Action: "mcp_install", Target: target, Apply: apply, Changes: diffSummary(string(original), updated)}
	if !apply {
		return planning, nil
	}
	backup, err := writeBackupAndAtomic(target, []byte(updated))
	if err != nil {
		return Plan{}, err
	}
	planning.Backup = backup
	return planning, nil
}

func SkillsPlan(sourceDir string, apply bool) (Plan, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Plan{}, err
	}
	if sourceDir == "" {
		sourceDir = filepath.Join("skills")
	}
	target := filepath.Join(home, ".codex", "skills", "violin")
	planning := Plan{Action: "skills_install", Target: target, Apply: apply, Changes: "install bundled Violin skills"}
	if !apply {
		return planning, nil
	}
	if info, err := os.Stat(target); err == nil {
		if info.IsDir() {
			if _, markerErr := os.Stat(filepath.Join(target, ".violin-managed")); markerErr != nil {
				return Plan{}, errors.New("unmanaged Violin skill directory exists; refusing to overwrite")
			}
		} else {
			return Plan{}, errors.New("unmanaged Violin skill path exists; refusing to overwrite")
		}
	} else if !os.IsNotExist(err) {
		return Plan{}, err
	}
	if _, err := os.Stat(target); err == nil {
		backup := target + ".backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
		if err := copyTree(target, backup); err != nil {
			return Plan{}, err
		}
		planning.Backup = backup
	}
	if _, err := os.Stat(sourceDir); err == nil {
		if err := copyTree(sourceDir, target); err != nil {
			return Plan{}, err
		}
	} else if os.IsNotExist(err) {
		if err := copyEmbeddedTree(target); err != nil {
			return Plan{}, err
		}
	} else {
		return Plan{}, err
	}
	if err := os.WriteFile(filepath.Join(target, ".violin-managed"), []byte("managed\n"), 0600); err != nil {
		return Plan{}, err
	}
	return planning, nil
}

func copyEmbeddedTree(target string) error {
	return walkEmbedded("assets", target)
}

func walkEmbedded(source, target string) error {
	entries, err := bundledSkills.ReadDir(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		return err
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		targetPath := filepath.Join(target, entry.Name())
		if entry.IsDir() {
			if err := walkEmbedded(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		data, err := bundledSkills.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(targetPath, data, 0600); err != nil {
			return err
		}
	}
	return nil
}

func replaceManagedBlock(original, block string) (string, error) {
	if strings.Contains(original, startMarker) {
		before, rest, _ := strings.Cut(original, startMarker)
		_, after, ok := strings.Cut(rest, endMarker)
		if !ok {
			return "", errors.New("incomplete managed MCP block; refusing to overwrite")
		}
		return before + block + after, nil
	}
	if strings.Contains(original, "[mcp_servers.violin]") {
		return "", errors.New("unmanaged violin MCP configuration exists; refusing to overwrite")
	}
	return strings.TrimRight(original, "\n") + "\n\n" + block + "\n", nil
}

func writeBackupAndAtomic(path string, data []byte) (string, error) {
	backupPath := ""
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if old, err := os.ReadFile(path); err == nil {
		backupPath = path + ".backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
		if err := os.WriteFile(backupPath, old, 0600); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return "", err
	}
	return backupPath, os.Rename(tmp, path)
}

func copyTree(source, target string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if info.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0600)
	})
}

func JSON(value any) ([]byte, error) { return json.MarshalIndent(value, "", "  ") }
func Platform() string               { return runtime.GOOS + "/" + runtime.GOARCH }

func diffSummary(before, after string) string {
	if before == after {
		return "no changes"
	}
	return fmt.Sprintf("replace %d bytes with %d bytes", len(before), len(after))
}
