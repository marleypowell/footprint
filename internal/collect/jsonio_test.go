package collect

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type jsonRow struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// Output files are indented JSON, with HTML characters kept as written and a final newline. They
// are written whole or not at all, making their directories as needed.
func TestWriteJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "org", "nested", "out.json")
	if err := writeJSON(path, jsonRow{Name: "<a & b>", Tags: []string{}}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if want := "{\n  \"name\": \"<a & b>\",\n  \"tags\": []\n}\n"; err != nil || string(got) != want {
		t.Errorf("writeJSON wrote %q, %v, want %q", got, err, want)
	}
	if fileExists(path + ".tmp") {
		t.Error("the temp file outlived the write")
	}
	if err := writeJSON(path, map[string]any{"bad": make(chan int)}); err == nil || !strings.Contains(err.Error(), "encoding "+path) {
		t.Errorf("writeJSON(unencodable) = %v, want an encoding error naming the file", err)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "<a & b>") {
		t.Errorf("a failed write replaced the previous file: %q", got)
	}
}

func TestWriteJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	if err := writeJSONL(path, []jsonRow{{Name: "a&b"}, {Name: "c", Tags: []string{"x"}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := `{"name":"a&b","tags":null}` + "\n" + `{"name":"c","tags":["x"]}` + "\n"; string(got) != want {
		t.Errorf("writeJSONL wrote %q, want one compact line per row: %q", got, want)
	}
	if err := writeJSONL(path, []jsonRow{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); len(got) != 0 {
		t.Errorf("writeJSONL(no rows) wrote %q, want an empty file", got)
	}
}

func TestReadJSON(t *testing.T) {
	dir := t.TempDir()
	var v jsonRow
	if ok, err := readJSON(filepath.Join(dir, "missing.json"), &v); ok || err != nil {
		t.Errorf("readJSON(missing) = %v, %v; want false, nil: a missing file is not an error", ok, err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"name":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := readJSON(bad, &v); ok || err == nil || !strings.Contains(err.Error(), "decoding "+bad) {
		t.Errorf("readJSON(corrupt) = %v, %v; want an error naming the file", ok, err)
	}
	good := filepath.Join(dir, "good.json")
	if err := writeJSON(good, jsonRow{Name: "a", Tags: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := readJSON(good, &v); !ok || err != nil || !reflect.DeepEqual(v, jsonRow{Name: "a", Tags: []string{"x"}}) {
		t.Errorf("readJSON(good) = %v, %v, %+v", ok, err, v)
	}
}

func TestReadJSONL(t *testing.T) {
	dir := t.TempDir()
	if rows, err := readJSONL[jsonRow](filepath.Join(dir, "missing.jsonl")); rows != nil || err != nil {
		t.Errorf("readJSONL(missing) = %v, %v; want no rows and no error", rows, err)
	}
	path := filepath.Join(dir, "rows.jsonl")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"name":"a"}` + "\n" + `{"name":"b"}` + "\n\n")
	if rows, err := readJSONL[jsonRow](path); err != nil || !reflect.DeepEqual(rows, []jsonRow{{Name: "a"}, {Name: "b"}}) {
		t.Errorf("readJSONL = %+v, %v", rows, err)
	}
	write(`{"name":"a"}` + "\n" + `{"name":`)
	if rows, err := readJSONL[jsonRow](path); rows != nil || err == nil || !strings.Contains(err.Error(), "decoding "+path) {
		t.Errorf("readJSONL(truncated) = %v, %v; want an error and no partial rows", rows, err)
	}
}

func TestWriteUnderRefusesEscapes(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base")
	escapes := []string{"../escape.txt", "/tmp/absolute.txt", "a/../../b.txt"}
	if runtime.GOOS == "windows" { // ordinary file names elsewhere
		escapes = append(escapes, `..\escape.txt`, `a/..\..\b.txt`, "C:/absolute.txt", "a:stream.txt", "aux/x.txt", "nul")
	}
	for _, p := range escapes {
		if written, err := writeUnder(base, p, []byte("x")); written || err != nil {
			t.Errorf("%q: written %v, %v", p, written, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(base)); len(entries) != 0 {
		t.Fatalf("refused paths still wrote %v", entries)
	}
	if written, err := writeUnder(base, "docs/index.md", []byte("x")); !written || err != nil || !fileExists(filepath.Join(base, "docs", "index.md")) {
		t.Errorf("normal path not written: %v", err)
	}
	// A device name with an extension is a device to some Windows versions and an ordinary file name
	// to others (Windows 11): it is refused, or written as a file under base, never to the device.
	for _, p := range []string{"nul.yaml", "con.txt"} {
		written, err := writeUnder(base, p, []byte("x"))
		if err != nil {
			t.Errorf("%q: %v", p, err)
			continue
		}
		if info, err := os.Lstat(filepath.Join(base, p)); written && (err != nil || !info.Mode().IsRegular()) {
			t.Errorf("%q: written, but not as a file under base (%v)", p, err)
		}
	}
}
