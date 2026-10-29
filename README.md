# footprint

Collects raw data about **your** work across an org: every commit you made (full message and
per-file stats) from the start of each repo's history, the pull requests you authored and the ones
you reviewed, what each
project is (service catalog entities, READMEs, CLAUDE.md, code host metadata), the issues you were
assigned or that your commits and PRs name (with comments, history and worklogs), the architecture
decision records (ADRs) you committed to, and the wiki pages you created or edited.

Collection writes raw JSON, and a separate aggregation phase decides what counts and makes something
of it (a CV, a performance review, a promotion case, ...): see [Using the data](#using-the-data).
[`docs/data-schema.md`](docs/data-schema.md) documents the raw output and the caveats aggregation has
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

`all` ends with `doctor`, which says whether the dataset is complete and what to run or change when
it is not (see [Completeness](#completeness)). Then open `data/raw/index.json`.

## Requirements

- Go 1.24 or newer.
- git 2.31 or newer. On a filesystem that ignores letter case (Windows, and macOS by default), use
  git 2.46 or newer: mirrors there keep their refs in git's reftable store, so a repo with refs that
  differ only in case (a tag `V1.0` beside `v1.0`) still mirrors. `mirror` moves mirrors made
  before to it.
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
| `reviews` | other people's PRs you reviewed or commented on, with your reviews and comments | GitHub: GraphQL search, ~7 min for 1,700 PRs |
| `commits` | your commits on any ref | local git, ~30 s |
| `catalog` | every catalog entity on every default branch | local git only |
| `projects` | per-repo `project.json`, docs, decision records (ADRs) you committed to, `data/raw/index.json`, and the issue tracker's projects | local, plus one project listing; indexes `issues/` and `wiki/` too |
| `verify` | compares with the code host's own commit attribution | GitHub: ~3 min (search rate limits) |
| `issues` | issues ever assigned to you, or named in your commits and PRs, with comments, history and worklogs, plus the stories, epics and initiatives above them | Jira: one request per issue, a few minutes for thousands; skipped without an `issue_tracker` section |
| `pages` | wiki content you created or edited, with versions and the body as Markdown | Confluence: CQL search, then each page; skipped without a `wiki` section |

With an `issue_tracker` section, `prs`, `commits`, `projects` and `pages` list the tracker's projects
(once per run) to recognise issue keys, so they need its token too: without the Atlassian token,
`all` stops at `prs`. `issues` and `pages` run last and refresh their own parts of `index.json`.

Every stage is idempotent and overwrites its own output. To refresh later, run
`mirror prs reviews commits projects verify issues pages`. Flags can go anywhere on the command line:

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
- `identity.not_me` (optional): emails or names in the `identities` report that you have confirmed
  are someone else, so `doctor` stops asking about them.
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
| code host | `codehost.Host` (account, repos, PRs, reviewed PRs, attributed commits, plus URL, ref and merge-message conventions) | `codehost/github` | GitLab |
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

`bin/footprint doctor` reads the files below, the mirror state and the config, and prints one line
per check (`PASS`, `WARN`, `FAIL` or `SKIP`) with the fix under it; `-json` prints the report as
JSON. It exits 1 when a check fails, and calls no tools, so it also works on a copy of a dataset.

| check | fails or warns when |
|---|---|
| `verify` | commits the code host credits to you are missing; names the emails to add |
| `identities` | an identity resembling you is neither configured nor in `identity.not_me` (a warning: only you can say whose it is) |
| `reviews` | the `reviews` stage never ran (a warning: reviews are then unknown, not none) |
| `issues` | keys failed to load (fail) or are hidden from the token (warning) |
| `pages` | a wiki is configured but `pages` never ran |
| `mirror` | a repo failed to mirror, or was never mirrored |
| `stale` | an output is older than what it was made from (commits after `verify`, a changed identity, mirrors fetched after `commits`), or the data is over 30 days old |
| `env` | a variable a configured tool authenticates with is unset (names only), or `.env` is readable by others |

The files it reads:

- `data/raw/org/identities.json` lists every author/committer/co-author identity matching
  `discovery_pattern`. An entry with `matched_by_config: null` that is really you means an email or
  name is missing from the config.
- `data/raw/org/verification.json` lists default-branch commits that the code host attributes to
  your account but that are missing from the dataset.
- `data/raw/issues/unresolved.json` lists the issue keys that could not be loaded, with a reason:
  `not_found` is mostly typos and deleted issues, `failed` means re-run `issues`, and many keys of one
  live project mean the token cannot see that project.

After a config fix, re-run `commits projects verify issues`.

## Using the data

An agent (Claude, or another) makes documents and answers from the data, in steps with a boundary
where judgement starts. Code decides what counts; the agent writes what the work meant; code checks
what it wrote. [`docs/facts-schema.md`](docs/facts-schema.md) has the rules.

```sh
bin/footprint facts                                   # what counts and how much -> data/facts/
bin/footprint facts -from 2025-04-01 -to 2026-03-31   # ... and that window's view
bin/footprint facts -since 2026-10-22                 # ... and the work to ask you about since then -> recent/
bin/footprint check data/drafts/review.md             # citations and numbers in a note or draft
```

- `facts` deduplicates the changes, groups the work into items (features under their epics, docs
  (wiki pages and ADRs), and per-quarter buckets of work naming no issue), and counts. It writes
  `overview.md`, `facts.json`, one JSONL file per kind of record, an evidence pack per item,
  `catalogue.md`, and `todo.txt`: the notes still to write. It reads only `data/raw`, the notes and
  the context (no config or tokens), so it still works on a copy of the dataset once access to the
  tools has ended.
- The agent writes one note per work item, from its pack, in `data/notes/`; a note is reused until
  its pack changes. It drafts documents in `data/drafts/` from the notes and the facts.
- `check` fails a note or draft whose citations (`[[PAY-62]]`, `[[repo#123]]`) name nothing, and
  lists the numbers that match no fact. `-plain` or `-links` writes the version to share.
- Your own words go in `data/context/`: `profile.md` (titles, dates, teams), `framework.md` (the
  career framework to map evidence to), `pay.md` (pay and market figures, for a pay rise case),
  `applications/<slug>/` (a job application: the job description, the CV you sent, the process and
  your debriefs), `items/<id>.md`
  (impact or corrections for one item, which join its pack), and `evidence/<slug>.md` (evidence from
  outside the tools, for the framework's criteria). Marking `framework.md` with `{#<id>}` and
  `{level=<id>}` makes `facts` report which criteria have evidence (`criteria.md`).

In Claude Code, the `footprint` skill ([`.claude/skills/footprint/`](.claude/skills/footprint/SKILL.md))
runs these steps to answer questions about your work, and to write a CV or LinkedIn profile, a
performance review, a promotion or pay rise case, an offboarding snapshot, or a job
application's paper trail and interview preparation. Its steps only use the CLI and files, so another agent can follow `SKILL.md` too.

| directory | written by | |
|---|---|---|
| `data/raw/` | the collection stages | the dataset |
| `data/facts/` | `footprint facts` | replaced whole on every run |
| `data/notes/` | the agent | one per work item |
| `data/context/` | you, or the agent recording your words | profile, framework, corrections |
| `data/drafts/` | the agent | documents; `check` writes `.final.md` beside them |

## Development

```sh
go test ./...    # unit tests, plus a full offline pipeline test over real git repos
go vet ./...
```

Each top-level `internal/` dir has a `README.md` with its purpose and boundaries, and every Go
file has a sibling `_test.go` that reads as its spec.

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
internal/facts/         aggregation: the counting rules, work items, packs, note states, check
internal/doctor/        completeness checks over a collected dataset
internal/filekind/      written vs generated or vendored files
internal/tech/          technologies named from file paths
docs/data-schema.md     the output contract (compiled in; written to data/raw/README.md)
docs/facts-schema.md    the facts contract (compiled in; written to data/facts/README.md)
.claude/skills/footprint/  the skill an agent follows to use the data
config.example.json     config template (compiled in; init copies its mirror settings and tool kinds)
assets.go               embeds the three compiled-in files
data/raw/               output: personal, gitignored (start at index.json)
data/facts/, notes/, context/, drafts/   aggregation: personal, gitignored
cache/                  git mirrors + status, gitignored, safe to delete
```

`data/` holds content from private repos, issues and wiki pages (commit messages, PR descriptions,
READMEs, comments) about one person. Keep it out of git and share it deliberately.
