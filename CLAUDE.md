# footprint

A Go CLI that collects raw data about one person's work across an org (commits on every ref,
their PRs, what each project is, the issues they worked on, the wiki pages they wrote), and then
decides what of it counts (`facts`, `check`) for an agent to make documents from. Setup, stages,
flags and config are in `README.md`.

- Build with `go build -o bin/footprint ./cmd/footprint`, and run it from the repo root
  (`config.json`, `data/raw` and `cache/` resolve against the working directory).
- The code host, catalog, issue tracker and wiki are seams: `internal/codehost`, `internal/catalog`,
  `internal/tracker` and `internal/wiki` each define an interface plus neutral types, with one
  adapter package beneath (github, backstage, jira, confluence). `internal/collect` depends on the
  interfaces only. Tool knowledge (API calls, annotation names, key formats, merge-message
  conventions, field ids) belongs in an adapter, and wiring belongs in `cmd/footprint/tools.go`.
  The jira and confluence adapters share `internal/atlassian`.
- The neutral types in those interface packages are also output records, so their JSON names are
  the data contract. `docs/data-schema.md` is that contract (embedded; `projects` writes it to
  `data/raw/README.md`). Change it in the same change as any output field.
- `config.json` is the person's identity. Generate it with `init --org <org>` for the authenticated
  user, then show them `identity.emails` and `identity.names` and get their confirmation before
  running `all`.
- A run is complete when `bin/footprint doctor` exits 0 and every identity its `identities` check
  lists is the user's (then configured) or someone else's (then in `identity.not_me`). It prints the
  fix under each finding. Its config snippets are suggestions: show them to the user and get their
  confirmation before adding them. `not_found` issue keys should be typos or deleted issues.
- The Jira and Confluence adapters need `ATLASSIAN_EMAIL` and `ATLASSIAN_API_TOKEN` (or per-product
  `JIRA_API_TOKEN` / `CONFLUENCE_API_TOKEN`): `issues` and `pages` load from them, and `prs`,
  `commits`, `projects` and `pages` list the Jira projects to recognise issue keys. The binary reads
  them from the repo's git-ignored `.env` (template: `.env.example`) or the environment. Only the
  user can create the token. Never print `.env` values or put them in commands; the binary logs
  variable names only. The Atlassian connector in Claude Code is for exploring and spot checks, not
  for collecting.
- `data/` (private-repo content about one person) and `cache/` (~9 GB of mirrors) stay untracked.
  So does `careers.json`, which lists the user's employers' datasets: never collect into one marked
  `frozen` (a past job's snapshot), and give each dataset its own cache (`README.md`: Several
  employers).
- The `issue_tracker` and `wiki` config sections are optional; code must handle a nil
  `Tools.Tracker` / `Tools.Wiki` (no issue keys, the `issues` / `pages` stage skipped).
- Verify changes with `go test ./...`. `TestPipeline` runs every stage offline through the in-memory
  adapters in `internal/collect/fakes_test.go`, over real git repos.
- Every `.go` file has a sibling `<name>_test.go` with unit tests of that file's code, added or
  extended in the same change. They are how a reader learns what the file decides, so write them to
  read as its spec: behaviour-named, table-driven cases that show the rules and edge cases. The only
  exceptions are files with nothing to unit-test (types and interfaces only). Shared fixtures and
  fakes live in the package's existing helper test files.
- Each top-level `internal/` dir has a `README.md` giving its purpose, boundaries and test approach.
  Update it only when one of those changes. Its files and functions are documented in the code.
- `internal/facts` is the code half of aggregation: pure and offline (it reads `data/raw`,
  `data/notes` and `data/context`, never config, tokens or mirrors). What counts is decided there,
  not in the skill or an agent's ad hoc queries. `docs/facts-schema.md` is its contract (embedded;
  `facts` writes it to `data/facts/README.md`): change it in the same change as any facts field or
  rule. The output must stay byte-identical for the same inputs (`TestDeterministic`).
- To use the data (questions, CV, review, promotion or pay rise case, offboarding, job applications), follow
  `.claude/skills/footprint/SKILL.md`. Numbers in documents come only from the facts or the user's
  own words in `data/context/`, and claims cite records that `footprint check` resolves.
