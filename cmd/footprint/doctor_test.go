package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/doctor"
)

// doctorOpts point at an empty data directory, a missing config and a missing .env.
func doctorOpts(t *testing.T) (collect.Options, string) {
	dir := t.TempDir()
	return collect.Options{ConfigPath: filepath.Join(dir, "config.json"), DataDir: filepath.Join(dir, "raw"),
		CacheDir: filepath.Join(dir, "cache")}, filepath.Join(dir, ".env")
}

// Without a config, doctor still reports on the dataset: here, that there is none, which fails.
func TestRunDoctorWithoutAConfigOrDataset(t *testing.T) {
	opts, env := doctorOpts(t)
	var err error
	out := captureStdout(t, func() { err = runDoctor(opts, env, false) })
	if !errors.Is(err, errIncomplete) {
		t.Errorf("runDoctor(no dataset) = %v, want %v", err, errIncomplete)
	}
	for _, want := range []string{"FAIL", "no index.json", "footprint all"} {
		if !strings.Contains(out, want) {
			t.Errorf("runDoctor(no dataset) printed:\n%s\nwant %q in it", out, want)
		}
	}
}

func TestRunDoctorWritesJSON(t *testing.T) {
	opts, env := doctorOpts(t)
	var err error
	out := captureStdout(t, func() { err = runDoctor(opts, env, true) })
	if !errors.Is(err, errIncomplete) {
		t.Errorf("runDoctor(-json, no dataset) = %v, want %v", err, errIncomplete)
	}
	var report doctor.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("runDoctor -json printed %q: %v", out, err)
	}
	if !report.Failed() || len(report.Checks) != 1 || report.Checks[0].ID != "dataset" {
		t.Errorf("runDoctor -json = %+v, want the one failed dataset check", report)
	}
}

// A config that exists but cannot be read is an error, not a verdict on the dataset.
func TestRunDoctorBadConfig(t *testing.T) {
	opts, env := doctorOpts(t)
	opts.ConfigPath = writeConfig(t, `{"org":"acme"}`)
	var err error
	out := captureStdout(t, func() { err = runDoctor(opts, env, false) })
	if err == nil || errors.Is(err, errIncomplete) || !strings.Contains(err.Error(), "login is missing") {
		t.Errorf("runDoctor(bad config) = %v, want the config's error", err)
	}
	if out != "" {
		t.Errorf("runDoctor(bad config) printed %q, want no report", out)
	}
}
