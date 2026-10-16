# footprint

Collects raw data about **your** work across a GitHub org: every commit you made (full message and
per-file stats) from the start of each repo's history, the pull requests you authored, and what
each project is (Backstage catalog entities, READMEs, CLAUDE.md, GitHub metadata).

This is the collection phase only. The output is raw JSON so that a separate aggregation phase
(a CV, a promotion case, a performance review, ...) can decide what counts.
`harness/DATA_README.md` documents the schema and the caveats aggregation has to handle.

## Quick start

```sh
gh auth status                                          # repo + read:org scopes, SSO-authorised for the org
python3 -I harness/scrape.py init --org acme            # writes harness/config.json for the gh user
$EDITOR harness/config.json                             # review identity.emails / identity.names
python3 -I harness/scrape.py all                        # ~15 min from cold
```

Then open `data/raw/index.json`, and check `data/raw/org/verification.json`:
`github_commits_missing_from_dataset` should be `0`.

## Requirements

- `gh` CLI, logged in (`gh auth login`) with `repo` and `read:org`. For orgs with SAML SSO,
  authorise the token for the org.
- git 2.31 or newer.
- Python 3.9+ with PyYAML. `-I` (isolated mode) ignores `pip install --user` packages, so install it
  for the interpreter itself: `sudo apt install python3-yaml`, or use a venv
  (`python3 -m venv .venv && .venv/bin/pip install -r requirements.txt`, then run `.venv/bin/python3 -I`).
- Disk space for bare mirrors of every non-empty repo in the org (several GB in `cache/` for a large org).

## Stages

`python3 -I harness/scrape.py <stage> [<stage> ...]`, or `all` to run them in this order:

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

Every stage is idempotent and overwrites its own output. Per-repo stages take
`--only repo-a,repo-b`. To refresh later, run `mirror prs commits projects verify`.

## Configuration

`harness/config.json` is personal and gitignored. `init` generates it from
`harness/config.example.json`:

- `identity.emails`: every address you have committed with, matched case-insensitively. `init` seeds
  this from the commits GitHub already attributes to your account. The GitHub noreply form is
  always matched.
- `identity.names`: full names matched exactly, for commits made with an email GitHub does not
  know. Remove any name you share with a colleague.
- `identity.discovery_pattern`: a regex used only by the `identities` report to find variants worth
  adding.
- `catalog_hub_repos`: repos holding Backstage entities for *other* repos. Entities found there are
  tied to a repo through annotations or links, not by where the file lives.
- `jira.projects_file`, `jira.extra_project_keys`: real Jira project keys, so `jira_keys` picks up
  `ABC-123` and skips look-alikes such as `UTF-8`. Leave both empty to switch Jira extraction off.

The example's catalog and Jira settings are acme's. `init` keeps them only when `--org`
matches and clears them for other orgs.

`harness/jira_projects.json` is a snapshot of acme's Jira projects (key, name, type), taken
with the Atlassian connector in Claude Code (`getVisibleJiraProjects`, `action=browse`). Archived
projects are not listed, which is why `extra_project_keys` exists.

## Completeness

- `data/raw/org/identities.json` lists every author/committer/co-author identity matching
  `discovery_pattern`. An entry with `matched_by_config: null` that is really you means an email or
  name is missing from the config.
- `data/raw/org/verification.json` lists default-branch commits that GitHub attributes to your
  account but that are missing from the dataset.

After a config fix, re-run `commits projects verify`.

## Layout

```
harness/scrape.py            the harness (single file, stdlib + PyYAML)
harness/config.example.json  config template with acme defaults
harness/jira_projects.json   acme Jira project keys
harness/DATA_README.md       output schema (copied to data/raw/README.md)
data/raw/                    output: personal, gitignored (start at index.json)
cache/                       git mirrors + logs, gitignored, safe to delete
```

`data/` holds content from private repos (commit messages, PR descriptions, READMEs) about one
person. Keep it out of git and share it deliberately.
