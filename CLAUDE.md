# footprint

A Go CLI that collects raw data about one person's work across an org (commits on every ref,
their PRs, what each project is). Aggregating it into summaries is a separate, later phase. Setup,
stages, flags and config are in `README.md`.

- Build with `go build -o bin/footprint ./cmd/footprint`, and run it from the repo root
  (`config.json`, `data/raw` and `cache/` resolve against the working directory).
- The code host, catalog and issue tracker are seams: `internal/codehost`, `internal/catalog` and
  `internal/tracker` each define an interface plus neutral types, with one adapter package
  beneath (github, backstage, jira). `internal/scrape` depends on the interfaces only. Tool
  knowledge (API calls, annotation names, key formats, merge-message conventions) belongs in an
  adapter, and wiring belongs in `cmd/footprint/tools.go`.
- The neutral types in those interface packages are also output records, so their JSON names are
  the data contract. `docs/data-schema.md` is that contract (embedded; `projects` writes it to
  `data/raw/README.md`). Change it in the same change as any output field.
- `config.json` is the person's identity. Generate it with `init --org <org>` for the authenticated
  user, then show them `identity.emails` and `identity.names` and get their confirmation before
  running `all`.
- A run is complete when `data/raw/org/verification.json` has
  `attributed_commits_missing_from_dataset: 0` and every `matched_by_config: null` entry in
  `data/raw/org/identities.json` is someone else. Fix gaps in the config, then re-run
  `commits projects verify`.
- `data/` (private-repo content about one person) and `cache/` (~9 GB of mirrors) stay untracked.
- `jira_projects.json` comes from the Atlassian connector: `getVisibleJiraProjects` with
  `action=browse` and `expandIssueTypes=false`, every page, keeping `key`, `name`, `projectTypeKey`,
  `archived`. The binary itself has no Jira access.
- Verify changes with `go test ./...`. `TestPipeline` runs every stage offline through the in-memory
  adapters in `internal/scrape/fakes_test.go`, over real git repos.
- Aggregation work starts by reading `docs/data-schema.md`: its role and duplicate rules (`patch_id`,
  squash vs original commits) decide what counts.
