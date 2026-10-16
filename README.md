# footprint

Collects raw data about **your** work across an org's repositories: every commit you made (full
message and per-file stats) from the start of each repo's history, the pull requests you authored,
and what each project is (service catalog entities, READMEs, CLAUDE.md, code host metadata).

This is the collection phase only. The output is raw JSON so that a separate aggregation phase
(a CV, a promotion case, a performance review, ...) can decide what counts.
[`docs/data-schema.md`](docs/data-schema.md) documents the output and the caveats aggregation has
to handle.

The code host, service catalog and issue tracker are swappable: today the adapters are GitHub,
Backstage and Jira (see [Swapping tools](#swapping-tools)).

## Quick start

```sh
go build -o bin/footprint ./cmd/footprint
gh auth status                                  # repo + read:org scopes, SSO-authorised for the org
bin/footprint init --org acme                   # writes config.json for the gh user
$EDITOR config.json                             # review identity.emails / identity.names
bin/footprint all                               # ~15 min from cold
```

Then open `data/raw/index.json`, and check `data/raw/org/verification.json`:
`attributed_commits_missing_from_dataset` should be `0`.

## Requirements

- Go 1.24 or newer.
- git 2.31 or newer.
- For the GitHub adapter: the `gh` CLI, logged in (`gh auth login`) with `repo` and `read:org`. For
  orgs with SAML SSO, authorise the token for the org. API calls use `gh auth token` (or
  `GH_TOKEN` / `GITHUB_TOKEN`), and git fetches authenticate through `gh auth git-credential`.
- Disk space for bare mirrors of every non-empty repo in the org (several GB in `cache/` for a large org).

## Stages

`footprint [flags] <stage>...` runs stages in the order given, and `all` runs them in this order:

| stage | does | notes |
|---|---|---|
| `inventory` | lists every org repo with code host metadata | GitHub: GraphQL |
| `mirror` | bare clone / incremental fetch of every non-empty repo into `cache/mirrors/` | branches, tags and change-request heads; ~3 min cold |
| `identities` | lists every identity resembling you, with counts | flags variants your config does not match |
| `prs` | your PRs with their commits and files | GitHub: GraphQL search, ~4 min |
| `commits` | your commits on any ref | local git only, ~30 s |
| `catalog` | every catalog entity on every default branch | local git only |
| `projects` | per-repo `project.json`, docs, and `data/raw/index.json` | local |
| `verify` | compares with the code host's own commit attribution | GitHub: ~3 min (search rate limits) |

Every stage is idempotent and overwrites its own output. To refresh later, run
`mirror prs commits projects verify`. Flags can go anywhere on the command line:

| flag | default | |
|---|---|---|
| `-config` | `config.json` | the personal config |
| `-data` | `data/raw` | output directory |
| `-cache` | `cache` | git mirrors and mirror status |
| `-only` | | comma-separated repos for `mirror` and `commits` |
| `-org`, `-code-host`, `-force` | | `init` only (`-code-host` defaults to `github`) |

## Configuration

`config.json` is personal and gitignored. `init` generates it from
[`config.example.json`](config.example.json), which is compiled into the binary:

- `org`, `login`: the org (GitHub org, GitLab group) and your account on the code host.
- `identity.emails`: every address you have committed with, matched case-insensitively. `init` seeds
  this from the commits the code host already attributes to your account, plus its noreply forms.
- `identity.names`: full names matched exactly, for commits made with an email the host does not
  know. Remove any name you share with a colleague.
- `identity.discovery_pattern`: a regex (valid as both RE2 and POSIX ERE) that the `identities`
  report uses to find variants worth adding, and that `commits` uses to find possible
  `Co-authored-by` trailers.
- `code_host`, `catalog`, `issue_tracker`: one section per tool. `kind` picks the adapter, and the
  rest of the section is that adapter's settings:
  - `github`: no settings.
  - `backstage`: `hub_repos`, the repos holding entities for *other* repos. Entities found there are
    tied to a repo through annotations or links, not by where the file lives.
  - `jira`: `projects_file` (relative to the config file) and `extra_project_keys`: the real project
    keys, so `issue_keys` picks up `ABC-123` and skips look-alikes such as `UTF-8`. With neither,
    no issue keys are extracted.
- `mirror`: `parallelism` and `fetch_pull_refs`.

The example's catalog and issue tracker settings are acme's. `init` keeps them only when
`--org` matches, and keeps just the `kind` for other orgs.

[`jira_projects.json`](jira_projects.json) is a snapshot of acme's Jira projects, taken with
the Atlassian connector in Claude Code (`getVisibleJiraProjects`, `action=browse`). Archived projects
are not listed, which is why `extra_project_keys` exists.

## Swapping tools

Each tool sits behind a Go interface, and the scraper core (`internal/scrape`) uses only those
interfaces. Adapters are wired up in `cmd/footprint/tools.go`, by the `kind` in each config
section:

| seam | interface | adapter today | expected next |
|---|---|---|---|
| code host | `codehost.Host` (account, repos, PRs, attributed commits, plus URL, ref and merge-message conventions) | `codehost/github` | GitLab |
| service catalog | `catalog.Catalog` (`Scan` one repo's default branch into neutral entities) | `catalog/backstage` | Cortex |
| issue tracker | `tracker.Tracker` (issue keys in text, the tracker's projects) | `tracker/jira` | Linear |

To add one, write a package beside the existing adapter that satisfies the interface, then add a
case to `cmd/footprint/tools.go`. The interface docs state the contracts. For example, a code
host's `Remote` must fetch change-request heads to `refs/pull/<n>/head` (GitLab:
`+refs/merge-requests/*/head:refs/pull/*/head`). The in-memory adapters in
`internal/scrape/fakes_test.go`, written in GitLab, Cortex and Linear style, show the shape, and
`TestPipeline` runs every stage through them.

## Completeness

- `data/raw/org/identities.json` lists every author/committer/co-author identity matching
  `discovery_pattern`. An entry with `matched_by_config: null` that is really you means an email or
  name is missing from the config.
- `data/raw/org/verification.json` lists default-branch commits that the code host attributes to
  your account but that are missing from the dataset.

After a config fix, re-run `commits projects verify`.

## Development

```sh
go test ./...    # unit tests, plus a full offline pipeline test over real git repos
go vet ./...
```

```
cmd/footprint/          CLI entry point; tools.go wires the adapters
internal/scrape/        the stages and git mirroring: the core, tool-neutral
internal/codehost/      code host interface + github adapter
internal/catalog/       catalog interface + backstage adapter
internal/tracker/       issue tracker interface + jira adapter
internal/doc/           raw documents: key-ordered JSON objects, YAML parsing
internal/logx/          progress logging
docs/data-schema.md     the output contract (compiled in; written to data/raw/README.md)
config.example.json     config template with acme defaults (compiled in; used by init)
jira_projects.json      acme Jira project keys
assets.go               embeds the two compiled-in files
data/raw/               output: personal, gitignored (start at index.json)
cache/                  git mirrors + status, gitignored, safe to delete
```

`data/` holds content from private repos (commit messages, PR descriptions, READMEs) about one
person. Keep it out of git and share it deliberately.
