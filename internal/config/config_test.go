package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFilesPrefersPublicConfigOverLegacy(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy.toml")
	public := filepath.Join(root, "public.toml")
	if err := os.WriteFile(legacy, []byte("[backend.qwen]\ntransport = \"cli\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(public, []byte("[backend.qwen]\ntransport = \"api\"\n[backend.qwen.api]\nbase_url = \"https://example.test\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := loadFiles([]string{legacy, public})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Backend["qwen"].Transport != "api" || settings.Backend["qwen"].API.BaseURL != "https://example.test" {
		t.Fatalf("settings=%+v", settings.Backend["qwen"])
	}
}

func TestDefaultsDoNotSelectLocalQwenRoute(t *testing.T) {
	backend := Defaults().Backend["qwen"]
	if backend.Transport != "auto" || backend.API.BaseURL != "" || len(backend.CLI.Command) != 0 {
		t.Fatalf("unsafe local default: %+v", backend)
	}
}

func TestCLICommandEnvironmentOverride(t *testing.T) {
	t.Setenv("VIOLIN_QWEN_CLI_COMMAND", `["custom-agent","--stdin"]`)
	settings, err := loadFiles([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Backend["qwen"].Transport != "cli" || settings.Backend["qwen"].CLI.Command[0] != "custom-agent" {
		t.Fatalf("settings=%+v", settings.Backend["qwen"])
	}
}

func TestIdleTimeoutPolicyCanDeclareBuiltInCLI(t *testing.T) {
	settings := Defaults()
	qwen := settings.Backend["qwen"]
	qwen.Transport = "cli"
	qwen.CLI.Command = []string{"violin-codex-qwen"}
	disabled := false
	qwen.IdleTimeoutEnabled = &disabled
	settings.Backend["qwen"] = qwen
	if IdleTimeoutEnabled("qwen", settings.Backend["qwen"]) {
		t.Fatal("built-in qwen CLI should not use idle timeout")
	}
}

func TestLayaConfigEnvironmentOverridesAreValidated(t *testing.T) {
	t.Setenv("VIOLIN_LAYA_MODE", "advisory")
	t.Setenv("VIOLIN_LAYA_RUNNER", `["laya-runtime"]`)
	settings, err := loadFiles([]string{})
	if err != nil || settings.Laya.Mode != "advisory" || len(settings.Laya.Runner) != 1 {
		t.Fatalf("settings=%+v err=%v", settings.Laya, err)
	}
	t.Setenv("VIOLIN_LAYA_MODE", "unsafe")
	if _, err := loadFiles([]string{}); err == nil {
		t.Fatal("expected invalid Laya mode rejection")
	}
}
