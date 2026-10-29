// The test is in an external package: internal/collect imports this one, so only footprint_test
// can import collect to decode the example config the way init and every stage do.
package footprint_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/marleypowell/footprint"
	"github.com/marleypowell/footprint/internal/collect"
)

func TestSchemasAreEmbedded(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"DataSchema", footprint.DataSchema},
		{"FactsSchema", footprint.FactsSchema},
	}
	for _, tt := range tests {
		// Written as README.md files, so each starts with a Markdown title.
		if !bytes.HasPrefix(tt.data, []byte("# ")) {
			t.Errorf("%s starts %q, want a Markdown title", tt.name, tt.data[:min(len(tt.data), 20)])
		}
	}
}

func TestExampleConfigIsValidJSON(t *testing.T) {
	if !json.Valid(footprint.ExampleConfig) {
		t.Fatalf("ExampleConfig is not valid JSON:\n%s", footprint.ExampleConfig)
	}
}

// LoadConfig rejects a config without the required fields, so the template init fills in must
// pass it, with every optional section present to show its shape.
func TestExampleConfigLoadsAsAConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, footprint.ExampleConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := collect.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(ExampleConfig) = %v", err)
	}
	if cfg.IssueTracker == nil || cfg.Wiki == nil {
		t.Errorf("ExampleConfig issue_tracker = %v, wiki = %v; want both sections, to show their shape", cfg.IssueTracker, cfg.Wiki)
	}
	if cfg.Mirror.Parallelism < 1 {
		t.Errorf("ExampleConfig mirror.parallelism = %d, want at least 1", cfg.Mirror.Parallelism)
	}
}
