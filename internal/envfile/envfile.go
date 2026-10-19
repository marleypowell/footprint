// Package envfile loads KEY=VALUE settings from a .env file into the process environment, for the
// tokens the adapters read (ATLASSIAN_API_TOKEN, GH_TOKEN, ...).
package envfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"runtime"
	"strings"
)

// Load sets each variable in the file that the environment does not already hold (or holds
// empty), so a variable exported in the shell wins over the file. Blank values in the file (a
// template's unfilled token) set nothing. A missing file loads nothing. It returns the names it
// set, never the values.
func Load(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	vars, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var set []string
	for _, v := range vars {
		if v.Value == "" || os.Getenv(v.Name) != "" {
			continue
		}
		if err := os.Setenv(v.Name, v.Value); err != nil {
			return nil, err
		}
		set = append(set, v.Name)
	}
	return set, nil
}

// TooOpen reports whether the file can be read by anyone but its owner. It is always false on
// Windows, where access is set by ACLs that Go's permission bits do not show.
func TooOpen(path string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().Perm()&0o077 != 0
}

// Var is one setting from a file.
type Var struct{ Name, Value string }

var nameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse reads the lines of a .env file: `KEY=value`, with an optional `export ` prefix, blank lines
// and # comments. Values may be unquoted (trailing ` # comment` stripped), 'single quoted'
// (literal) or "double quoted" (with \n, \t, \" and \\ escapes). Nothing is expanded.
func Parse(text string) ([]Var, error) {
	var vars []Var
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, raw, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !nameRE.MatchString(name) {
			return nil, fmt.Errorf("line %d: want NAME=value", i+1) // never echo the line: it may hold a secret
		}
		value, err := unquote(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %w", i+1, name, err)
		}
		vars = append(vars, Var{name, value})
	}
	return vars, nil
}

func unquote(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	switch raw[0] {
	case '\'':
		end := strings.IndexByte(raw[1:], '\'')
		if end < 0 {
			return "", errors.New("unterminated single quote")
		}
		return raw[1 : 1+end], nil
	case '"':
		var b strings.Builder
		for i := 1; i < len(raw); i++ {
			switch c := raw[i]; c {
			case '"':
				return b.String(), nil
			case '\\':
				if i++; i == len(raw) {
					return "", errors.New("unterminated double quote")
				}
				switch raw[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(raw[i])
				}
			default:
				b.WriteByte(c)
			}
		}
		return "", errors.New("unterminated double quote")
	}
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw), nil
}
