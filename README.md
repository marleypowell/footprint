# footprint

Collects raw data about **your** work across an org: every commit you made (full message and
per-file stats) from the start of each repo's history, the pull requests you authored, what each
project is (service catalog entities, READMEs, CLAUDE.md, code host metadata), the issues you were
assigned or that your commits and PRs name (with comments, history and worklogs), and the wiki pages
you created or edited.

This is the collection phase only. The output is raw JSON so that a separate aggregation phase
(a CV, a promotion case, a performance review, ...) can decide what counts.
[`docs/data-schema.md`](docs/data-schema.md) documents the output and the caveats aggregation has
to handle.

The code host, service catalog, issue tracker and wiki are swappable: today the adapters are GitHub,
Backstage, Jira and Confluence (see [Swapping tools](#swapping-tools)).

## Quick start

```sh
go build -o bin/footprint ./cmd/footprint
gh auth status                                  # repo + read:org scopes, SSO-authorised for the org
bin/footprint init --org acme                   # writes config.json for the gh user
$EDITOR config.json                             # review identity.emails / identity.names
cp .env.example .env && chmod 600 .env          # then add your Atlassian email and API token
bin/footprint all                               # ~20 min from cold for an org of about 250 repos
```

On Windows, in PowerShell (the `.exe` matters: Windows will not run the binary without it):

```powershell
go build -o bin/footprint.exe ./cmd/footprint
gh auth status
bin/footprint init --org acme
notepad config.json
Copy-Item .env.example .env                     # your user profile is private already; no chmod
bin/footprint all
```

Or install it with `go install github.com/marleypowell/footprint/cmd/footprint@latest` and run
`footprint` from a directory of your own: `config.json`, `.env`, `data/` and `cache/` all resolve
against the working directory. Without a clone there is no `.env.example` to copy, so create `.env`
with `ATLASSIAN_EMAIL` and `ATLASSIAN_API_TOKEN`.

Then open `data/raw/index.json`, and check `data/raw/org/verification.json`:
`attributed_commits_missing_from_dataset` should be `0`.

## Requirements

- Go 1.24 or newer.
- git 2.31 or newer.
- For the GitHub adapter: the `gh` CLI, logged in (`gh auth login`) with `repo` and `read:org`. For
  orgs with SAML SSO, authorise the token for the org. API calls use `gh auth token` (or
  `GH_TOKEN` / `GITHUB_TOKEN`), and git fetches authenticate through `gh auth git-credential`.
- For the Jira and Confluence adapters: an Atlassian API token for your account, from
  [id.atlassian.com](https://id.atlassian.com/manage-profile/security/api-tokens), in
  `ATLASSIAN_API_TOKEN`, with your Atlassian email in `ATLASSIAN_EMAIL`. Put them in a `.env` file
  in the working directory (see [`.env.example`](.env.example); it is git-ignored, and `-env` names
  another file) or export them; exported variables win over the file. A classic token works for
  both products. A token with scopes is made for one product, so set `JIRA_API_TOKEN` and
  `CONFLUENCE_API_TOKEN` instead. Read-only scopes are enough:
  - Jira: `read:jira-work`, `read:jira-user`.
  - Confluence: `search:confluence`, `read:confluence-user`, `read:page:confluence`,
    `read:blogpost:confluence`, `read:user:confluence`, plus `read:whiteboard:confluence`,
    `read:database:confluence`, `read:folder:confluence` and `read:embed:confluence` for owners of
    those types.

  Scoped tokens work only through Atlassian's API gateway; the client finds it on its own.
- Disk space for bare mirrors of every non-empty repo in the org (several GB in `cache/` for a large org).

## Stages

`footprint [flags] <stage>...` runs stages in the order given, and `all` runs them in this order
(timings are for an org of about 250 repos):

| stage | does | notes |
|---|---|---|
| `inventory` | lists every org repo with code host metadata | GitHub: GraphQL |
| `mirror` | bare clone / incremental fetch of every non-empty repo into `cache/mirrors/` | branches, tags and change-request heads; ~3 min cold |
| `identities` | lists every identity resembling you, with counts | flags variants your config does not match |
| `prs` | your PRs with their commits and files | GitHub: GraphQL search, ~4 min |
| `commits` | your commits on any ref | local git, ~30 s |
| `catalog` | every catalog entity on every default branch | local git only |
| `projects` | per-repo `project.json`, docs, `data/raw/index.json`, and the issue tracker's projects | local, plus one project listing; indexes `issues/` and `wiki/` too |
| `verify` | compares with the code host's own commit attribution | GitHub: ~3 min (search rate limits) |
| `issues` | issues ever assigned to you, or named in your commits and PRs, with comments, history and worklogs | Jira: one request per issue, a few minutes for thousands; skipped without an `issue_tracker` section |
| `pages` | wiki content you created or edited, with versions and the body as Markdown | Confluence: CQL search, then each page; skipped without a `wiki` section |

With an `issue_tracker` section, `prs`, `commits` and `projects` list the tracker's projects (once
per run) to recognise issue keys, so they need its token too: without the Atlassian token, `all`
stops at `prs`. `issues` and `pages` run last and refresh their own parts of `index.json`.

Every stage is idempotent and overwrites its own output. To refresh later, run
`mirror prs commits projects verify issues pages`. Flags can go anywhere on the command line:

| flag | default | |
|---|---|---|
| `-config` | `config.json` | the personal config |
| `-env` | `.env` | file of `KEY=value` tokens loaded into the environment |
| `-data` | `data/raw` | output directory |
| `-cache` | `cache` | git mirrors and mirror status |
| `-only` | | comma-separated repos for `mirror` and `commits` |
| `-org`, `-code-host`, `-force` | | `init` only (`-code-host` defaults to `github`) |

## Configuration

`config.json` is personal and gitignored. `init` generates it from
[`config.example.json`](config.example.json), which is compiled into the binary:

- `org`, `login`: the org (a GitHub organization) and your account on the code host.
- `identity.emails`: every address you have committed with, matched case-insensitively. `init` seeds
  this from the commits the code host already attributes to your account, plus its noreply forms.
- `identity.names`: full names matched exactly, for commits made with an email the host does not
  know. Remove any name you share with a colleague.
- `identity.discovery_pattern`: a regex (valid as both RE2 and POSIX ERE) that the `identities`
  report uses to find variants worth adding, and that `commits` uses to find possible
  `Co-authored-by` trailers.
- `code_host`, `catalog`, `issue_tracker`, `wiki`: one section per tool. The issue tracker and wiki
  are optional: without an `issue_tracker`, commits and PRs have no `issue_keys` and the `issues`
  stage does nothing; without a `wiki`, the `pages` stage does nothing. `kind` picks the adapter, and
  the rest of the section is that adapter's settings:
  - `github`: no settings.
  - `backstage`: `hub_repos`, the repos holding entities for *other* repos. Entities found there are
    tied to a repo through annotations or links, not by where the file lives.
  - `jira`: `site` (`example.atlassian.net`), and optionally `extra_project_keys`. `issue_keys`
    picks up `ABC-123` only for real projects, and skips look-alikes such as `UTF-8`. The adapter
    lists the site's live and archived projects, with the keys renamed projects had before.
    `extra_project_keys` adds keys the API cannot list, such as those of deleted projects that issues
    were moved out of.
  - `confluence`: `site`.
- `mirror`: `parallelism` and `fetch_pull_refs`.

`init` copies the example's `mirror` settings and the `kind` of each tool. The example's other tool
settings only show the shape: add your org's (at least the Jira and Confluence `site`), or delete
the `issue_tracker` and `wiki` sections.

## Swapping tools

Each tool sits behind a Go interface, and the collection core (`internal/collect`) uses only those
interfaces. Adapters are wired up in `cmd/footprint/tools.go`, by the `kind` in each config
section:

| seam | interface | adapter today | expected next |
|---|---|---|---|
| code host | `codehost.Host` (account, repos, PRs, attributed commits, plus URL, ref and merge-message conventions) | `codehost/github` | GitLab |
| service catalog | `catalog.Catalog` (`Scan` one repo's default branch into neutral entities) | `catalog/backstage` | Cortex |
| issue tracker | `tracker.Tracker` (issue keys in text, the tracker's projects; the account, the issues ever assigned to it, issues loaded by key) | `tracker/jira` | Linear |
| wiki | `wiki.Wiki` (the account, and everything it created or edited) | `wiki/confluence` | Notion |

To add one, write a package beside the existing adapter that satisfies the interface, then add a
case to `cmd/footprint/tools.go`. The interface docs state the contracts. For example, a code
host's `Remote` must fetch change-request heads to `refs/pull/<n>/head` (GitLab:
`+refs/merge-requests/*/head:refs/pull/*/head`). The in-memory adapters in
`internal/collect/fakes_test.go`, written in GitLab, Cortex, Linear and Notion style, show the shape,
and `TestPipeline` runs every stage through them. The Jira and Confluence adapters share
`internal/atlassian` (authentication, retries, and rendering Atlassian's rich text as Markdown).

## Completeness

- `data/raw/org/identities.json` lists every author/committer/co-author identity matching
  `discovery_pattern`. An entry with `matched_by_config: null` that is really you means an email or
  name is missing from the config.
- `data/raw/org/verification.json` lists default-branch commits that the code host attributes to
  your account but that are missing from the dataset.
- `data/raw/issues/unresolved.json` lists the issue keys that could not be loaded, with a reason:
  `not_found` is mostly typos and deleted issues, `failed` means re-run `issues`, and many keys of one
  live project mean the token cannot see that project.

After a config fix, re-run `commits projects verify issues`.

## Development

```sh
go test ./...    # unit tests, plus a full offline pipeline test over real git repos
go vet ./...
```

CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs the tests with `-race` on Go 1.24
and the latest Go, plus gofmt, staticcheck, govulncheck, and `go vet` for macOS and Windows.

```
cmd/footprint/          CLI entry point; tools.go wires the adapters
internal/collect/       the stages and git mirroring: the core, tool-neutral
internal/codehost/      code host interface + github adapter
internal/catalog/       catalog interface + backstage adapter
internal/tracker/       issue tracker interface + jira adapter
internal/wiki/          wiki interface + confluence adapter
internal/atlassian/     the Atlassian Cloud client the jira and confluence adapters share; adf/ renders rich text
internal/envfile/       .env loading
internal/doc/           raw documents: key-ordered JSON objects, YAML parsing
internal/par/           bounded parallel work
internal/logx/          progress logging
docs/data-schema.md     the output contract (compiled in; written to data/raw/README.md)
config.example.json     config template (compiled in; init copies its mirror settings and tool kinds)
assets.go               embeds the two compiled-in files
data/raw/               output: personal, gitignored (start at index.json)
cache/                  git mirrors + status, gitignored, safe to delete
```

`data/` holds content from private repos, issues and wiki pages (commit messages, PR descriptions,
READMEs, comments) about one person. Keep it out of git and share it deliberately.
