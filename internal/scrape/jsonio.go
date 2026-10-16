package scrape

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// marshal encodes v as compact JSON without HTML escaping: the output is data, not web content.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// writeFileAtomic writes via a temp file and rename, so readers never see a half-written file.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeJSON(path string, v any) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return writeFileAtomic(path, b.Bytes())
}

func writeJSONL[T any](path string, rows []T) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return fmt.Errorf("encoding %s: %w", path, err)
		}
	}
	return writeFileAtomic(path, b.Bytes())
}

// readJSON decodes path into v. ok is false, with no error, when the file does not exist.
func readJSON(path string, v any) (ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("decoding %s: %w", path, err)
	}
	return true, nil
}

// readJSONL decodes one value per line. A missing file reads as no rows.
func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []T
	dec := json.NewDecoder(f)
	for {
		var row T
		err := dec.Decode(&row)
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decoding %s: %w", path, err)
		}
		rows = append(rows, row)
	}
}

// writeUnder writes a repo-controlled relative path below base, refusing anything that would
// escape it.
func writeUnder(base, relpath string, data []byte) error {
	if strings.HasPrefix(relpath, "/") || slices.Contains(strings.Split(relpath, "/"), "..") {
		logf("refusing to write suspicious path %q under %s", relpath, base)
		return nil
	}
	path := filepath.Join(base, filepath.FromSlash(relpath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// orderedMap is a JSON object that keeps insertion order (encoding/json sorts map keys), so YAML
// documents and ranked counts keep their order in the output.
type orderedMap struct {
	keys   []string
	values map[string]any
}

func newOrderedMap() *orderedMap { return &orderedMap{values: map[string]any{}} }

func (m *orderedMap) set(key string, value any) {
	if _, ok := m.values[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

func (m *orderedMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := marshal(m.values[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// field returns obj[key] for a decoded JSON or YAML object, and nil for anything else, so
// lookups can chain through missing or mistyped values.
func field(obj any, key string) any {
	switch m := obj.(type) {
	case *orderedMap:
		if m != nil {
			return m.values[key]
		}
	case map[string]any:
		return m[key]
	}
	return nil
}

func isObject(v any) bool {
	switch m := v.(type) {
	case *orderedMap:
		return m != nil
	case map[string]any:
		return true
	}
	return false
}

func asString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	default:
		return fmt.Sprint(s)
	}
}
