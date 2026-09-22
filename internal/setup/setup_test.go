package setup

import (
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
	if _, err := ConfigPlan(true); err == nil {
		t.Fatal("expected existing config refusal")
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
