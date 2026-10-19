// Package footprint embeds the files the footprint binary ships with, so init, the projects stage
// and facts work from any directory.
package footprint

import _ "embed"

// ExampleConfig is the config template that `footprint init` fills in for a user. init keeps its
// mirror settings and the kind of each tool; its other tool settings only show the shape.
//
//go:embed config.example.json
var ExampleConfig []byte

// DataSchema documents the output. The projects stage writes it to <data>/README.md.
//
//go:embed docs/data-schema.md
var DataSchema []byte

// FactsSchema documents the facts. The facts command writes it to <facts>/README.md.
//
//go:embed docs/facts-schema.md
var FactsSchema []byte
