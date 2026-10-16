// Package footprint embeds the files the footprint binary ships with, so init and the
// projects stage work from any directory.
package footprint

import _ "embed"

// ExampleConfig is the config template that `footprint init` fills in for a user. Its catalog
// and Jira settings are acme's.
//
//go:embed config.example.json
var ExampleConfig []byte

// DataSchema documents the output. The projects stage writes it to <data>/README.md.
//
//go:embed docs/data-schema.md
var DataSchema []byte
