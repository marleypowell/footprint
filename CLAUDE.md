# footprint

A Go CLI that collects raw data about one person's work across a GitHub org (commits on every ref,
their PRs, what each project is). Aggregating it into summaries is a separate, later phase. Setup,
stages and flags are in `README.md`.

- Build with `go build -o bin/footprint ./cmd/footprint`, and run it from the repo root
  (`config.json`, `data/raw` and `cache/` resolve against the working directory).
- `config.json` is the person's identity. Generate it with `init --org <org>` for the
  `gh`-authenticated user, then show them `identity.emails` and `identity.names` and get their
  confirmation before running `all`.
- A run is complete when `data/raw/org/verification.json` has `github_commits_missing_from_dataset: 0`
  and every `matched_by_config: null` entry in `data/raw/org/identities.json` is someone else. Fix gaps
  in the config, then re-run `commits projects verify`.
- `data/` (private-repo content about one person) and `cache/` (~9 GB of mirrors) stay untracked.
- `jira_projects.json` comes from the Atlassian connector: `getVisibleJiraProjects` with
  `action=browse` and `expandIssueTypes=false`, every page, keeping `key`, `name`, `projectTypeKey`,
  `archived`. The binary itself has no Jira access.
- `docs/data-schema.md` is the output contract (embedded; `projects` writes it to `data/raw/README.md`).
  Change it in the same change as any output field. The output format matches the earlier Python
  harness's, so datasets from either version read the same.
- Verify changes with `go test ./...`. `TestExtractCommits` builds real repos with git, covering every
  role, renames, binaries, cherry-picks, merges, tags and PR refs.
- Aggregation work starts by reading `docs/data-schema.md`: its role and duplicate rules (`patch_id`,
  squash vs original commits) decide what counts.
