package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestCommonModelAndCredentialSettingsOverrideLegacyAPIFields(t *testing.T) {
	settings, err := loadFiles([]string{})
	if err != nil {
		t.Fatal(err)
	}
	backend := settings.Backend["qwen"]
	backend.Model = "common-model"
	backend.API.Model = "legacy-model"
	backend.APIKeyEnv = "COMMON_KEY"
	backend.API.APIKeyEnv = "LEGACY_KEY"
	if backend.EffectiveModel() != "common-model" || backend.EffectiveAPIKeyEnv() != "COMMON_KEY" {
		t.Fatalf("effective settings model=%q key_env=%q", backend.EffectiveModel(), backend.EffectiveAPIKeyEnv())
	}
	backend.Model = ""
	backend.APIKeyEnv = ""
	if backend.EffectiveModel() != "legacy-model" || backend.EffectiveAPIKeyEnv() != "LEGACY_KEY" {
		t.Fatalf("legacy compatibility model=%q key_env=%q", backend.EffectiveModel(), backend.EffectiveAPIKeyEnv())
	}
}

func TestBackendAuthModeMustMatchTransport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[backend.claude]\ntransport = \"cli\"\nauth = \"api_key\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "auth must match its transport") {
		t.Fatalf("expected clear auth/transport error, got %v", err)
	}
}

func TestLegacyCLIModelMigratesToSharedSettingInMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := "[backend.claude]\ntransport = \"cli\"\n[backend.claude.cli]\ncommand = [\"claude\", \"--model\", \"legacy-sonnet\"]\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	backend := settings.Backend["claude"]
	if backend.Model != "legacy-sonnet" || backend.CLI.Command[2] != "{model}" {
		t.Fatalf("legacy config was not normalized: %+v", backend)
	}
}

func TestCLIModelPlaceholderRequiresSharedModelSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := "[backend.claude]\ntransport = \"cli\"\n[backend.claude.cli]\ncommand = [\"claude\", \"--model\", \"{model}\"]\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "backend.claude.model is required") {
		t.Fatalf("expected missing model error, got %v", err)
	}
}

func TestCodexUsesTheCommonCLIModelSettingsAndStaysOptIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := "[backend.codex]\nmodel = \"gpt-test\"\nauth = \"cli\"\ntransport = \"cli\"\n[backend.codex.cli]\ncommand = [\"codex\", \"exec\", \"--model\", \"{model}\"]\nstatus_command = [\"codex\", \"login\", \"status\"]\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	backend := settings.Backend["codex"]
	if backend.EffectiveModel() != "gpt-test" || backend.Auth != "cli" || backend.CLI.Command[3] != "{model}" {
		t.Fatalf("unexpected Codex config: %+v", backend)
	}
	for _, name := range settings.Scheduler.Order {
		if name == "codex" {
			t.Fatal("Codex must remain opt-in until explicitly added to scheduler.order")
		}
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

func TestQwenCodeProfileUsesQwenCLIAndPerModeApproval(t *testing.T) {
	profile := filepath.Join("..", "..", "examples", "profiles", "violin-lan-qwen.toml")
	settings, err := loadFiles([]string{profile})
	if err != nil {
		t.Fatal(err)
	}
	backend := settings.Backend["qwen"]
	if backend.Command == nil || backend.Transport != "" || backend.Protocol != "qwen" || backend.Model != "qwen3.8-27b" || backend.Auth != "cli" {
		t.Fatalf("unexpected Qwen Code backend config: %+v", backend)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"\"qwen\"", "--safe-mode", "--approval-mode", "{approval_mode}", "--model", "{model}", "stream-json"} {
		if !strings.Contains(text, want) {
			t.Errorf("Qwen Code profile missing %q", want)
		}
	}
	if strings.Contains(text, "\"codex\"") || strings.Contains(text, "violin_local") {
		t.Fatalf("Qwen Code profile must not route through a Codex Qwen profile:\n%s", text)
	}
}

func TestIdleTimeoutPolicyCanDeclareQwenCodeCLI(t *testing.T) {
	settings := Defaults()
	qwen := settings.Backend["qwen"]
	qwen.Command = []any{"qwen"}
	disabled := false
	qwen.IdleTimeoutEnabled = &disabled
	settings.Backend["qwen"] = qwen
	if IdleTimeoutEnabled("qwen", settings.Backend["qwen"]) {
		t.Fatal("Qwen Code CLI should use its hard timeout only")
	}
}

func TestLayaConfigEnvironmentOverridesAreValidated(t *testing.T) {
	t.Setenv("VIOLIN_LAYA_MODE", "advisory")
	settings, err := loadFiles([]string{})
	if err != nil || settings.Laya.Mode != "advisory" {
		t.Fatalf("settings=%+v err=%v", settings.Laya, err)
	}
	t.Setenv("VIOLIN_LAYA_MODE", "unsafe")
	if _, err := loadFiles([]string{}); err == nil {
		t.Fatal("expected invalid Laya mode rejection")
	}
}
