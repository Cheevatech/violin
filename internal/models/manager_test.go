package models

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallVerifyAndRollback(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("model bundle")
	if err := os.WriteFile(filepath.Join(source, "model.bin"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	m, err := NewManager(filepath.Join(root, "models"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: DefaultModelID, Version: "v1", Source: "test", Revision: "r1", Runtime: "test", Language: DefaultLanguage, Artifacts: []Artifact{{Path: "model.bin", SHA256: hex.EncodeToString(hash[:])}}}
	if err = m.Install(manifest, source, true); err != nil {
		t.Fatal(err)
	}
	if err = m.Verify(); err != nil {
		t.Fatal(err)
	}
	if got := m.Status().Active; got != "v1" {
		t.Fatalf("active=%q", got)
	}
	if err = m.Rollback("missing"); err == nil {
		t.Fatal("expected rollback failure")
	}
}

func TestInstallRejectsNonEnglishModel(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	_ = os.Mkdir(source, 0700)
	_ = os.WriteFile(filepath.Join(source, "model.bin"), []byte("model"), 0600)
	m, _ := NewManager(filepath.Join(root, "models"))
	if err := m.Install(Manifest{ID: "laya-multilingual", Version: "v1", Language: "multi", Artifacts: []Artifact{{Path: "model.bin"}}}, source, true); err == nil {
		t.Fatal("expected non-English model rejection")
	}
}

func TestInstallRejectsMissingLanguage(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	_ = os.Mkdir(source, 0700)
	_ = os.WriteFile(filepath.Join(source, "model.bin"), []byte("model"), 0600)
	m, _ := NewManager(filepath.Join(root, "models"))
	if err := m.Install(Manifest{ID: DefaultModelID, Version: "v1", Artifacts: []Artifact{{Path: "model.bin"}}}, source, true); err == nil {
		t.Fatal("expected missing language rejection")
	}
}

func TestVerifyRejectsTamperedArtifact(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	_ = os.Mkdir(source, 0700)
	_ = os.WriteFile(filepath.Join(source, "model.bin"), []byte("good"), 0600)
	m, _ := NewManager(filepath.Join(root, "models"))
	if err := m.Install(Manifest{ID: "laya", Version: "v1", Artifacts: []Artifact{{Path: "model.bin", SHA256: "00"}}}, source, true); err == nil {
		t.Fatal("expected checksum failure")
	}
}

func TestModelVersionPathsRejectTraversal(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	_ = os.Mkdir(source, 0700)
	_ = os.WriteFile(filepath.Join(source, "model.bin"), []byte("model"), 0600)
	m, _ := NewManager(filepath.Join(root, "models"))
	for _, version := range []string{"../escape", `..\escape`, "/absolute", ""} {
		err := m.Install(Manifest{ID: DefaultModelID, Version: version, Language: DefaultLanguage, Artifacts: []Artifact{{Path: "model.bin"}}}, source, false)
		if err == nil {
			t.Fatalf("expected unsafe version rejection for %q", version)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "models", "active"), []byte("../escape\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ActivePath(); err == nil {
		t.Fatal("expected unsafe active version rejection")
	}
}
