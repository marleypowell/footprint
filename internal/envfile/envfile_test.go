package envfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	got, err := Parse("# tokens\n\nATLASSIAN_EMAIL=jane@example.com\r\nexport A=1 # trailing comment\n" +
		"B = spaced out \nC='lit #eral $x'\nD=\"a \\\"quoted\\\" line\\nnext\"\nE=\nF=has#hash\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Var{
		{"ATLASSIAN_EMAIL", "jane@example.com"}, {"A", "1"}, {"B", "spaced out"}, {"C", "lit #eral $x"},
		{"D", "a \"quoted\" line\nnext"}, {"E", ""}, {"F", "has#hash"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseErrorsNeverEchoTheLine(t *testing.T) {
	for _, bad := range []string{"not an assignment s3cr3t", "1BAD=x", "A='open s3cr3t", "A=\"open s3cr3t"} {
		_, err := Parse(bad)
		if err == nil || strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("Parse(%q) error %v: want an error without the secret", bad, err)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("FROM_FILE=file\nFROM_SHELL=file\nEMPTY_IN_SHELL=file\nBLANK_IN_FILE=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FROM_SHELL", "shell")
	t.Setenv("EMPTY_IN_SHELL", "")
	os.Unsetenv("FROM_FILE")
	os.Unsetenv("BLANK_IN_FILE")
	t.Cleanup(func() { os.Unsetenv("FROM_FILE") })

	set, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(set, []string{"FROM_FILE", "EMPTY_IN_SHELL"}) {
		t.Errorf("set %v", set)
	}
	if _, found := os.LookupEnv("BLANK_IN_FILE"); found {
		t.Error("a blank value in the file should set nothing")
	}
	if os.Getenv("FROM_FILE") != "file" || os.Getenv("FROM_SHELL") != "shell" || os.Getenv("EMPTY_IN_SHELL") != "file" {
		t.Error("the shell's variables should win, except empty ones")
	}
	if TooOpen(path) {
		t.Error("0600 is private")
	}
	if err := os.Chmod(path, 0o644); err != nil || !TooOpen(path) {
		t.Error("0644 should be flagged")
	}
	if set, err := Load(filepath.Join(t.TempDir(), "absent")); set != nil || err != nil {
		t.Errorf("a missing file loads nothing: %v %v", set, err)
	}
}
