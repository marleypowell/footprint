package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/doctor"
)

// errIncomplete is doctor's verdict when a check failed; the report says why.
var errIncomplete = errors.New("the dataset is incomplete: see the FAIL lines above")

// runDoctor reports on the dataset, as text or JSON on stdout. The config is optional: without
// it, doctor uses the identity and tools the dataset recorded.
func runDoctor(opts collect.Options, envPath string, asJSON bool) error {
	cfg, err := collect.LoadConfig(opts.ConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		cfg, err = nil, nil
	}
	if err != nil {
		return err
	}
	in := doctor.Inputs{DataDir: opts.DataDir, CacheDir: opts.CacheDir, Config: cfg, Now: time.Now()}
	if _, err := os.Stat(envPath); err == nil {
		in.EnvFile = envPath
	}
	if cfg != nil {
		in.Env = envNeeds(cfg)
	}
	report, err := doctor.Run(in)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		err = enc.Encode(report)
	} else {
		err = report.WriteText(os.Stdout)
	}
	if err != nil {
		return err
	}
	if report.Failed() {
		return errIncomplete
	}
	return nil
}
