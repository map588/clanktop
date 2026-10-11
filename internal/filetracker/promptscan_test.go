package filetracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanPromptFiles(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	projectDir := filepath.Join(home, ".claude", "projects", "-x")
	for _, p := range []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".claude", "rules", "a.md"),
		filepath.Join(cwd, "CLAUDE.md"),
		filepath.Join(projectDir, "memory", "MEMORY.md"),
		filepath.Join(projectDir, "s1.jsonl"), // A session log is not a prompt file.
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := ScanPromptFiles(home, cwd, projectDir)
	if len(got) != 4 {
		t.Fatalf("got %d files: %+v", len(got), got)
	}
	for _, f := range got {
		if filepath.Ext(f.Path) != ".md" {
			t.Fatalf("unexpected file %s", f.Path)
		}
	}
}
