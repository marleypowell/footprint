# footprint

Collects raw data about **your** work across a GitHub org: every commit you made (full message and
per-file stats) from the start of each repo's history, the pull requests you authored, and what
each project is (Backstage catalog entities, READMEs, CLAUDE.md, GitHub metadata).

This is the collection phase only. The output is raw JSON so that a separate aggregation phase
(a CV, a promotion case, a performance review, ...) can decide what counts.
[`docs/data-schema.md`](docs/data-schema.md) documents the output and the caveats aggregation has
to handle.

## Quick start

```sh
go build -o bin/footprint ./cmd/footprint
gh auth status                                  # repo + read:org scopes, SSO-authorised for the org
bin/footprint init --org acme                   # writes config.json for the gh user
$EDITOR config.json                             # review identity.emails / identity.names
bin/footprint all                               # ~15 min from cold
```

Then open `data/raw/index.json`, and check `data/raw/org/verification.json`:
`github_commits_missing_from_dataset` should be `0`.

## Requirements

- Go 1.24 or newer.
- The `gh` CLI, logged in (`gh auth login`) with `repo` and `read:org`. For orgs with SAML SSO,
  authorise the token for the org. API calls use `gh auth token` (or `GH_TOKEN` / `GITHUB_TOKEN`),
  and git fetches authenticate through `gh auth git-credential`.
- git 2.31 or newer.
- Disk space for bare mirrors of every non-empty repo in the org (several GB in `cache/` for a large org).

## Stages

`footprint [flags] <stage>...` runs stages in the order given, and `all` runs them in this order:

| stage | does | notes |
|---|---|---|
| `inventory` | lists every org repo with GitHub metadata | GraphQL |
| `mirror` | bare clone / incremental fetch of every non-empty repo into `cache/mirrors/` | branches, tags and `refs/pull/*/head`; ~3 min cold |
| `identities` | lists every identity resembling you, with counts | flags variants your config does not match |
| `prs` | your PRs with their commits and files | GraphQL search, ~4 min |
| `commits` | your commits on any ref | local git only, ~30 s |
| `catalog` | every Backstage entity on every default branch | local git only |
| `projects` | per-repo `project.json`, docs, and `data/raw/index.json` | local |
| `verify` | compares with GitHub's own commit attribution | ~3 min (search rate limits) |

Every stage is idempotent and overwrites its own output. To refresh later, run
`mirror prs commits projects verify`. Flags can go anywhere on the command line:

| flag | default | |
|---|---|---|
| `-config` | `config.json` | the personal config |
| `-data` | `data/raw` | output directory |
| `-cache` | `cache` | git mirrors and mirror status |
| `-only` | | comma-separated repos for `mirror` and `commits` |
| `-org`, `-force` | | `init` only |

## Configuration

`config.json` is personal and gitignored. `init` generates it from
[`config.example.json`](config.example.json), which is compiled into the binary:

- `identity.emails`: every address you have committed with, matched case-insensitively. `init` seeds
  this from the commits GitHub already attributes to your account. The GitHub noreply form is
  always matched.
- `identity.names`: full names matched exactly, for commits made with an email GitHub does not
  know. Remove any name you share with a colleague.
- `identity.discovery_pattern`: an RE2/ERE regex that the `identities` report uses to find variants
  worth adding, and that `commits` uses to find possible `Co-authored-by` trailers.
- `catalog_hub_repos`: repos holding Backstage entities for *other* repos. Entities found there are
  tied to a repo through annotations or links, not by where the file lives.
- `jira.projects_file` (relative to the config file), `jira.extra_project_keys`: real Jira project
  keys, so `jira_keys` picks up `ABC-123` and skips look-alikes such as `UTF-8`. Leave both empty to
  switch Jira extraction off.

The example's catalog and Jira settings are acme's. `init` keeps them only when `--org`
matches and clears them for other orgs.

[`jira_projects.json`](jira_projects.json) is a snapshot of acme's Jira projects (key, name,
type), taken with the Atlassian connector in Claude Code (`getVisibleJiraProjects`, `action=browse`).
Archived projects are not listed, which is why `extra_project_keys` exists.

## Completeness

- `data/raw/org/identities.json` lists every author/committer/co-author identity matching
  `discovery_pattern`. An entry with `matched_by_config: null` that is really you means an email or
  name is missing from the config.
- `data/raw/org/verification.json` lists default-branch commits that GitHub attributes to your
  account but that are missing from the dataset.

After a config fix, re-run `commits projects verify`.

## Development

```sh
go test ./...    # unit tests, plus an integration test that builds real git repos
go vet ./...
```

```
cmd/footprint/          CLI entry point
internal/scrape/        the stages, one file each, plus git/GitHub/JSON/YAML helpers
docs/data-schema.md     the output contract (compiled in; written to data/raw/README.md)
config.example.json     config template with acme defaults (compiled in; used by init)
jira_projects.json      acme Jira project keys
assets.go               embeds the two compiled-in files
data/raw/               output: personal, gitignored (start at index.json)
cache/                  git mirrors + status, gitignored, safe to delete
```

`data/` holds content from private repos (commit messages, PR descriptions, READMEs) about one
person. Keep it out of git and share it deliberately.
