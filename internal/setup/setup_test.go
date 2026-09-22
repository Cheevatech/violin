package setup

import "testing"

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
