package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReplaceManagedBlockIsIdempotent(t *testing.T) {
	block := startMarker + "\nvalue\n" + endMarker
	first, err := replaceManagedBlock("base\n", block)
	if err != nil {
		t.Fatal(err)
	}
	second, err := replaceManagedBlock(first, block)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("not idempotent:\n%s\n---\n%s", first, second)
	}
}

func TestReplaceManagedBlockRejectsUnmanagedConfig(t *testing.T) {
	if _, err := replaceManagedBlock("[mcp_servers.violin]\ncommand = \"custom\"\n", "block"); err == nil {
		t.Fatal("expected unmanaged config rejection")
	}
}

func TestRemoveManagedBlockPreservesSurroundingConfig(t *testing.T) {
	original := "before\n" + startMarker + "\nmanaged\n" + endMarker + "\nafter\n"
	updated, changed, err := removeManagedBlock(original)
	if err != nil || !changed || updated != "before\nafter\n" {
		t.Fatalf("updated=%q changed=%v err=%v", updated, changed, err)
	}
	if _, changed, err := removeManagedBlock("unmanaged\n"); err != nil || changed {
		t.Fatalf("unexpected unmanaged result changed=%v err=%v", changed, err)
	}
}

func TestConfigPlanCreatesCredentialFreeTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	plan, err := ConfigPlan(false)
	if err != nil || plan.Action != "config_init" || plan.Apply {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	plan, err = ConfigPlan(true)
	if err != nil || !plan.Apply {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "violin", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || containsCredentialMarker(string(data)) {
		t.Fatalf("unsafe config template: %s", data)
	}
	if !strings.Contains(string(data), "[laya]\nmode = \"advisory\"") {
		t.Fatalf("new config must keep Laya advisory: %s", data)
	}
	if _, err := ConfigPlan(true); err == nil {
		t.Fatal("expected existing config refusal")
	}
}

func TestLayaAdvisoryPlanBacksUpActiveConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".config", "violin", "config.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	original := "[laya]\nmode = \"active\"\ntimeout_seconds = 10\n"
	if err := os.WriteFile(target, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := LayaAdvisoryPlan(false)
	if err != nil || plan.Apply || plan.Backup != "" {
		t.Fatalf("dry-run plan=%+v err=%v", plan, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != original {
		t.Fatalf("dry run changed config: data=%q err=%v", data, err)
	}
	plan, err = LayaAdvisoryPlan(true)
	if err != nil || !plan.Apply || plan.Backup == "" {
		t.Fatalf("apply plan=%+v err=%v", plan, err)
	}
	updated, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(updated), "mode = \"advisory\"") {
		t.Fatalf("active policy was not lowered: data=%q err=%v", updated, err)
	}
	backup, err := os.ReadFile(plan.Backup)
	if err != nil || string(backup) != original {
		t.Fatalf("original config backup mismatch: data=%q err=%v", backup, err)
	}
}

func TestEmbeddedSkillsDescribeCurrentGoMCPAndLayaContract(t *testing.T) {
	checks := map[string][]string{
		"assets/SKILL.md": {"name: violin", "laya_route", "laya_wait_job", "read-only/advisory", "checksum"},
	}
	for path, markers := range checks {
		data, err := bundledSkills.ReadFile(path)
		if err != nil {
			t.Fatalf("read embedded skill %s: %v", path, err)
		}
		for _, marker := range markers {
			if !strings.Contains(string(data), marker) {
				t.Errorf("embedded skill %s missing marker %q", path, marker)
			}
		}
	}
}

func TestEmbeddedManifestContainsOnlyUnifiedViolinSkill(t *testing.T) {
	data, err := bundledSkills.ReadFile("assets/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Skills []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Skills) != 1 || manifest.Skills[0].Name != "violin" || manifest.Skills[0].Path != "SKILL.md" {
		t.Fatalf("manifest=%+v", manifest.Skills)
	}
}

func TestRemoveLegacySkillDirectoriesOnlyTouchesKnownManagedPaths(t *testing.T) {
	target := t.TempDir()
	for _, name := range append(append([]string{}, legacySkillDirectories...), "custom") {
		if err := os.MkdirAll(filepath.Join(target, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name, "SKILL.md"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeLegacySkillDirectories(target); err != nil {
		t.Fatal(err)
	}
	for _, name := range legacySkillDirectories {
		if _, err := os.Stat(filepath.Join(target, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy directory remains: %s (%v)", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "custom", "SKILL.md")); err != nil {
		t.Fatalf("custom skill was modified: %v", err)
	}
}

func TestSkillsPlanMigratesManagedInstallWithBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".codex", "skills", "violin")
	if err := os.MkdirAll(filepath.Join(target, "violin-review"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "violin-review", "SKILL.md"), []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, ".violin-managed"), []byte("managed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("name: violin"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), []byte(`{"skills":[{"name":"violin","path":"SKILL.md"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := SkillsPlan(source, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Backup == "" {
		t.Fatal("managed migration must create a backup")
	}
	if _, err := os.Stat(plan.Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "violin-review")); !os.IsNotExist(err) {
		t.Fatalf("legacy skill remains: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "SKILL.md")); err != nil || string(data) != "name: violin" {
		t.Fatalf("unified skill not installed: data=%q err=%v", data, err)
	}
}

func containsCredentialMarker(value string) bool {
	for _, marker := range []string{"API_KEY =", "BEGIN PRIVATE KEY", "LLMUX_API_KEY ="} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`).MatchString(value)
}
