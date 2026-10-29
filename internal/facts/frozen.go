package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Frozen is <facts>/frozen.json: a facts directory kept as the record of a past job, once its raw
// data may be gone. Its hashes say the files are as the freeze left them.
type Frozen struct {
	FrozenAt     string            `json:"frozen_at"`
	Dataset      string            `json:"dataset"`
	FactsVersion int               `json:"facts_version"`
	PackVersion  int               `json:"pack_version"`
	Packs        bool              `json:"packs"` // the packs were kept, so notes can still be revised
	Files        map[string]string `json:"files"` // every file, "/"-separated, -> its SHA-256
}

const frozenFile = "frozen.json"

// Freeze makes a written facts directory a frozen record: it removes the packs when asked, then
// hashes every file into frozen.json.
func Freeze(dir, dataset string, at time.Time, dropPacks bool) (*Frozen, error) {
	if ok, err := readJSON(filepath.Join(dir, "facts.json"), &Summary{}); err != nil || !ok {
		return nil, errors.Join(err, fmt.Errorf("%s has no facts to freeze: run facts first", dir))
	}
	if dropPacks {
		if err := os.RemoveAll(filepath.Join(dir, "packs")); err != nil {
			return nil, err
		}
	}
	files, err := hashFiles(dir)
	if err != nil {
		return nil, err
	}
	fr := &Frozen{FrozenAt: at.UTC().Format(time.RFC3339), Dataset: dataset, FactsVersion: FactsVersion, PackVersion: PackVersion,
		Packs: !dropPacks, Files: files}
	data, err := marshal(fr)
	if err != nil {
		return nil, err
	}
	return fr, os.WriteFile(filepath.Join(dir, frozenFile), data, 0o644)
}

// VerifyFrozen checks a frozen facts directory against its hashes: a file changed, gone, or
// added since the freeze is an error. A directory never frozen passes.
func VerifyFrozen(dir string) error {
	var fr Frozen
	ok, err := readJSON(filepath.Join(dir, frozenFile), &fr)
	if err != nil || !ok {
		return err
	}
	files, err := hashFiles(dir)
	if err != nil {
		return err
	}
	var problems []string
	for _, name := range sortedKeys(fr.Files) {
		switch got, ok := files[name]; {
		case !ok:
			problems = append(problems, name+" is gone")
		case got != fr.Files[name]:
			problems = append(problems, name+" changed")
		}
	}
	for _, name := range sortedKeys(files) {
		if _, ok := fr.Files[name]; !ok {
			problems = append(problems, name+" was added")
		}
	}
	if len(problems) == 0 {
		return nil
	}
	const shown = 5
	more := ""
	if len(problems) > shown {
		more = fmt.Sprintf(", and %d more", len(problems)-shown)
		problems = problems[:shown]
	}
	return fmt.Errorf("%s was frozen on %s and is not as it was: %s%s", dir, fr.FrozenAt, strings.Join(problems, ", "), more)
}

// hashFiles hashes every file under dir but frozen.json, by its "/"-separated path.
func hashFiles(dir string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); rel == frozenFile {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	return files, err
}
