# footprint

Collects raw data about one person's work across a GitHub org (commits on every ref, their PRs,
what each project is). Aggregating it into summaries is a separate, later phase. Setup and stages
are in `README.md`.

- Run the harness as `python3 -I harness/scrape.py ...` from the repo root. PyYAML has to be installed
  for that interpreter (system package or venv), because `-I` skips user site-packages.
- `harness/config.json` is the person's identity. Generate it with `init --org <org>` for the
  `gh`-authenticated user, then show them `identity.emails` and `identity.names` and get their
  confirmation before running `all`.
- A run is complete when `data/raw/org/verification.json` has `github_commits_missing_from_dataset: 0`
  and every `matched_by_config: null` entry in `data/raw/org/identities.json` is someone else. Fix gaps
  in the config, then re-run `commits projects verify`.
- `data/` (private-repo content about one person) and `cache/` (~9 GB of mirrors) stay untracked.
- `harness/jira_projects.json` comes from the Atlassian connector: `getVisibleJiraProjects` with
  `action=browse` and `expandIssueTypes=false`, every page, keeping `key`, `name`, `projectTypeKey`. The
  harness itself has no Jira access.
- `harness/DATA_README.md` is the output contract (`projects` copies it to `data/raw/README.md`).
  Update it in the same change as any output field.
- Aggregation work starts by reading `harness/DATA_README.md`: its role and duplicate rules
  (`patch_id`, squash vs original commits) decide what counts.
