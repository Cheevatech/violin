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
