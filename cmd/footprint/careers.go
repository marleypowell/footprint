package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/careers"
	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/facts"
	"github.com/marleypowell/footprint/internal/logx"
)

// applyDataset points the paths at a dataset: every path flag the command line did not set takes
// the dataset's. It returns the dataset, or nil without -dataset.
func applyDataset(careersPath, id string, set map[string]bool, opts *collect.Options, fo *factsOptions, envPath *string) (*careers.Dataset, error) {
	if id == "" {
		return nil, nil
	}
	f, err := careers.Load(careersPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("-dataset %s needs %s, which lists the datasets (README.md: Several employers)", id, careersPath)
	}
	if err != nil {
		return nil, err
	}
	e, err := f.Get(id)
	if err != nil {
		return nil, err
	}
	if e.Root == "" {
		return nil, fmt.Errorf("%s has no dataset (no root in %s): its work is in your own words, in the career context", id, careersPath)
	}
	p := e.Paths()
	for flag, target := range map[string]*string{
		"config": &opts.ConfigPath, "data": &opts.DataDir, "cache": &opts.CacheDir, "env": envPath,
		"facts": &fo.dir, "notes": &fo.notes, "context": &fo.context,
	} {
		if !set[flag] {
			*target = map[string]string{"config": p.Config, "data": p.Data, "cache": p.Cache, "env": p.Env,
				"facts": p.Facts, "notes": p.Notes, "context": p.Context}[flag]
		}
	}
	return e, nil
}

// refuseFrozen stops collection, or a rebuild of the facts, in a frozen dataset: named
// with -dataset, or by its directory with -data.
func refuseFrozen(careersPath string, e *careers.Dataset, dataDir string) error {
	if e == nil && careers.Exists(careersPath) {
		f, err := careers.Load(careersPath)
		if err != nil {
			return err
		}
		e = f.FrozenAt(dataDir)
	}
	if e != nil && e.Frozen {
		return fmt.Errorf("%s is frozen: its dataset is a snapshot of a past job, and this would change it (%s: frozen); "+
			"check reads its facts as they are", e.ID, careersPath)
	}
	return nil
}

// runDatasets lists the datasets, the jobs by when the user started, then the personal ones: id,
// employer, months (or "personal"), the kind of
// dataset on disk, frozen or not, and its directory, tab-separated.
func runDatasets(w io.Writer, careersPath string) error {
	f, err := careers.Load(careersPath)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no %s: without it the dataset under data/ is the only one", careersPath)
	}
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, e := range f.Ordered() {
		to, frozen := e.To, ""
		if to == "" {
			to = "now"
		}
		months := e.From + ".." + to
		if e.Personal {
			months = "personal"
			if e.From != "" {
				months += " " + e.From + ".." + to
			}
		}
		if e.Frozen {
			frozen = "frozen"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n", e.ID, e.Employer, months, e.Kind(), frozen, e.Root)
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// freezeOptions are freeze's: what to remove once the facts are frozen.
type freezeOptions struct {
	dropRaw, dropPacks bool
}

// runFreeze keeps a past job's facts as its record: it builds them from the raw data a last time
// (when there is any), freezes the facts directory (frozen.json), marks the dataset frozen in the
// careers file, and then removes the packs or the raw data only when asked.
func runFreeze(careersPath string, e *careers.Dataset, opts collect.Options, fo factsOptions, fz freezeOptions, at time.Time) error {
	if e == nil {
		return errors.New("freeze needs -dataset <id>: the dataset to keep as a record")
	}
	if e.Personal {
		return fmt.Errorf("%s is personal: only a past job's dataset is frozen", e.ID)
	}
	if _, err := os.Stat(filepath.Join(fo.dir, "frozen.json")); err == nil {
		return fmt.Errorf("%s is frozen already (%s)", e.ID, filepath.Join(fo.dir, "frozen.json"))
	}
	if _, err := os.Stat(filepath.Join(opts.DataDir, "index.json")); err == nil {
		if err := runFacts(opts.DataDir, fo); err != nil {
			return err
		}
	} else {
		logx.Printf("freeze: %s has no raw data; freezing the facts in %s as they are", opts.DataDir, fo.dir)
	}
	fr, err := facts.Freeze(fo.dir, e.ID, at, fz.dropPacks)
	if err != nil {
		return err
	}
	f, err := careers.Load(careersPath)
	if err != nil {
		return err
	}
	if err := f.SetFrozen(e.ID); err != nil {
		return err
	}
	if err := f.Save(careersPath); err != nil {
		return err
	}
	logx.Printf("freeze: %s: %d files in %s, hashed in frozen.json; %s marks it frozen", e.ID, len(fr.Files), fo.dir, careersPath)
	if fz.dropRaw {
		if err := os.RemoveAll(opts.DataDir); err != nil {
			return err
		}
		logx.Printf("freeze: removed the raw data, %s", opts.DataDir)
	}
	return nil
}
