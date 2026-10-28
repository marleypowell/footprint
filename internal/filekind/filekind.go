// Package filekind tells the files a person wrote from those a tool made or that came from
// elsewhere, by path and size. Line counts leave the second kind out. Collection and facts share it,
// so they agree on what is written.
package filekind

import (
	"path"
	"strings"
)

// BulkLines is the size above which one file's change is treated as generated or vendored (a test
// fixture, an API dump, a moved tree) rather than written.
const BulkLines = 5000

// Bulky says whether one file's change is too large to have been written.
func Bulky(added, deleted int) bool { return added+deleted > BulkLines }

var (
	// lockfiles
	generatedNames = map[string]bool{
		"package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "bun.lockb": true,
		"go.sum": true, "cargo.lock": true, "gemfile.lock": true, "composer.lock": true, "poetry.lock": true,
		"pipfile.lock": true, "uv.lock": true, "packages.lock.json": true, "paket.lock": true, "podfile.lock": true,
	}
	// minified files, source maps, test snapshots, and the code designers, protobuf and Entity
	// Framework migrations write
	generatedSuffixes = []string{".min.js", ".min.css", ".map", ".snap",
		".designer.cs", ".g.cs", ".g.i.cs", ".pb.go", "_pb2.py", "modelsnapshot.cs"}
	// directories of other people's code, or of build output, wherever they sit
	vendoredDirs = map[string]bool{"vendor": true, "node_modules": true, "third_party": true, "dist": true}
)

// Generated says whether a file, by its path ("/"-separated, as git writes it), was made by a tool
// or copied in: a lockfile, minified or generated code, or anything in a vendored or build
// directory.
func Generated(p string) bool {
	base := strings.ToLower(path.Base(p))
	if generatedNames[base] {
		return true
	}
	for _, s := range generatedSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	dirs := strings.Split(strings.ToLower(path.Dir(p)), "/")
	for _, d := range dirs {
		if vendoredDirs[d] {
			return true
		}
	}
	return false
}
