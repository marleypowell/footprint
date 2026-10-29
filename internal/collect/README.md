# collect

The collection phase: the stages that `footprint` runs (`inventory`, `mirror`, `commits`, `prs`,
`issues`, `pages`, ...) and the config that names the person and the tools. It owns the git mirrors
under `cache/` and the raw output under `data/raw/`.

It talks to the code host, catalog, issue tracker and wiki only through their interfaces
(`internal/codehost`, `internal/catalog`, `internal/tracker`, `internal/wiki`), so it holds no tool
knowledge. The tracker and wiki are optional, and every stage handles their absence. Each stage is
idempotent and overwrites only its own output. The output contract is `docs/data-schema.md`.

Deciding what counts is not collection's job. That belongs in `internal/facts`.

Tests use the in-memory adapters in `fakes_test.go` over real git repos made in a temp dir.
`TestPipeline` runs every stage end to end, and the per-file tests cover each stage's rules.
