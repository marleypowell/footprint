package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/marleypowell/footprint/internal/careers"
	"github.com/marleypowell/footprint/internal/collect"
)

// applyEmployer points the paths at an employer's dataset: every path flag the command line did
// not set takes the employer's. It returns the employer, or nil without -employer.
func applyEmployer(careersPath, id string, set map[string]bool, opts *collect.Options, fo *factsOptions, envPath *string) (*careers.Employer, error) {
	if id == "" {
		return nil, nil
	}
	f, err := careers.Load(careersPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("-employer %s needs %s, which lists the employers (README.md: Several employers)", id, careersPath)
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

// refuseFrozen stops collection into a frozen employer's dataset: named with -employer, or by its
// directory with -data.
func refuseFrozen(careersPath string, e *careers.Employer, dataDir string) error {
	if e == nil && careers.Exists(careersPath) {
		f, err := careers.Load(careersPath)
		if err != nil {
			return err
		}
		e = f.FrozenAt(dataDir)
	}
	if e != nil && e.Frozen {
		return fmt.Errorf("%s is frozen: its dataset is a snapshot of a past job, and collecting would change it (%s: frozen)", e.ID, careersPath)
	}
	return nil
}

// runEmployers lists the employers, by when the user started: id, employer, months, the kind of
// dataset on disk, frozen or not, and its directory, tab-separated.
func runEmployers(w io.Writer, careersPath string) error {
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
		if e.Frozen {
			frozen = "frozen"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s..%s\t%s\t%s\t%s\n", e.ID, e.Employer, e.From, to, e.Kind(), frozen, e.Root)
	}
	_, err = io.WriteString(w, b.String())
	return err
}
