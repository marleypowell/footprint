package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Freeze hashes every file of a written facts directory into frozen.json, after removing the packs
// when asked; VerifyFrozen then finds any file changed, gone or added, and LoadStored refuses it.
func TestFreeze(t *testing.T) {
	_, _, dir := storedPair(t)
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	fr, err := Freeze(dir, "acme", at, false)
	if err != nil {
		t.Fatal(err)
	}
	if fr.FrozenAt != "2026-10-29T12:00:00Z" || fr.Dataset != "acme" || fr.FactsVersion != FactsVersion || !fr.Packs {
		t.Errorf("frozen = %+v", fr)
	}
	for _, name := range []string{"facts.json", "events.jsonl", "packs/ENG-1.md"} {
		if fr.Files[name] == "" {
			t.Errorf("no hash of %s", name)
		}
	}
	if _, ok := fr.Files[frozenFile]; ok {
		t.Error("frozen.json hashes itself")
	}
	if err := VerifyFrozen(dir); err != nil {
		t.Fatalf("VerifyFrozen() after Freeze = %v", err)
	}
	if _, err := LoadStored(dir, Inputs{}); err != nil {
		t.Fatalf("LoadStored(frozen) = %v", err)
	}

	tests := []struct {
		name   string
		change func(t *testing.T, dir string)
		want   string
	}{
		{"a file changed", func(t *testing.T, dir string) { appendTo(t, filepath.Join(dir, "items.jsonl"), "\n") }, "items.jsonl changed"},
		{"a file gone", func(t *testing.T, dir string) { remove(t, filepath.Join(dir, "todo.txt")) }, "todo.txt is gone"},
		{"a file added", func(t *testing.T, dir string) { appendTo(t, filepath.Join(dir, "windows", "x.md"), "x") }, "windows/x.md was added"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copyDir := filepath.Join(t.TempDir(), "facts")
			if err := os.CopyFS(copyDir, os.DirFS(dir)); err != nil {
				t.Fatal(err)
			}
			tt.change(t, copyDir)
			if err := VerifyFrozen(copyDir); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("VerifyFrozen() = %v, want an error with %q", err, tt.want)
			}
			if _, err := LoadStored(copyDir, Inputs{}); err == nil || !strings.Contains(err.Error(), "is not as it was") {
				t.Errorf("LoadStored() = %v, want it refused", err)
			}
		})
	}
}

// Without its packs a frozen record keeps the facts and the notes' citations, not what to revise
// a note from.
func TestFreezeDroppingPacks(t *testing.T) {
	_, _, dir := storedPair(t)
	fr, err := Freeze(dir, "acme", time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	if fr.Packs || fr.Files["packs/ENG-1.md"] != "" {
		t.Errorf("packs kept: %v", fr.Packs)
	}
	if _, err := os.Stat(filepath.Join(dir, "packs")); err == nil {
		t.Error("packs/ is still there")
	}
	if err := VerifyFrozen(dir); err != nil {
		t.Errorf("VerifyFrozen() = %v", err)
	}
	if _, err := Freeze(t.TempDir(), "acme", time.Now(), false); err == nil || !strings.Contains(err.Error(), "no facts to freeze") {
		t.Errorf("Freeze(empty) = %v", err)
	}
	if err := VerifyFrozen(t.TempDir()); err != nil {
		t.Errorf("VerifyFrozen(never frozen) = %v", err)
	}
}

func appendTo(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, p string) {
	t.Helper()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
}
