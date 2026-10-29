# facts

The code half of aggregation: it reads the collected dataset (`data/raw`), plus the user's
`data/notes` and `data/context`, and decides what counts and how much. That covers deduplicated
changes, work items, docs, repos, reviews, technologies, the career-framework map and totals, with
a bounded evidence pack per work item. `check` holds a note or draft to those facts, and `count`
and `search` answer questions from them without writing anything.

Rules about what counts live here, so every document an agent writes agrees on the numbers. The
package is pure and offline: it reads no config, tokens or mirrors, and the same inputs give
byte-identical output. The output contract is `docs/facts-schema.md`, which changes in the same
change as any field or rule.

Tests build a dataset in memory from the fixtures in `facts_test.go`. Each file's rules are tested
in its own `_test.go`, and `TestDeterministic` guards byte-identical output.
