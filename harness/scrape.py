#!/usr/bin/env python3
"""Footprint harness: raw data about one person's work across a GitHub org.

Stages (each is idempotent; run in this order, or `all`):
  inventory   every repo in the org with GitHub metadata      -> data/raw/org/repos.json
  mirror      bare clone/fetch of every non-empty repo         -> cache/mirrors/<repo>.git
              (branches, tags and refs/pull/*/head, so commits from
              squash-merged PRs with deleted branches are still reachable)
  identities  every author/committer/co-author identity that looks like the user
                                                               -> data/raw/org/identities.json
  prs         pull requests authored by the user               -> data/raw/repos/<repo>/prs.jsonl
  commits     the user's commits on any ref (author, committer, co-author, or in one of the
              user's PRs), full message + per-file stats       -> data/raw/repos/<repo>/commits.jsonl
  catalog     every Backstage entity on every default branch   -> data/raw/backstage/
  projects    per-repo "what is this" bundle + top-level index -> data/raw/repos/<repo>/project.json,
                                                                  data/raw/index.json
  verify      cross-check against GitHub's commit attribution  -> data/raw/org/verification.json

Setup:  python3 -I harness/scrape.py init --org <github-org>   writes harness/config.json for the
                                                                gh-authenticated user
Usage:  python3 -I harness/scrape.py <stage> [<stage> ...]
        python3 -I harness/scrape.py all
        python3 -I harness/scrape.py mirror commits --only repo-a,repo-b
"""

import argparse
import concurrent.futures as cf
import datetime as dt
import json
import os
import re
import shutil
import subprocess
import sys
import time
from collections import Counter, defaultdict
from pathlib import Path

import yaml

HARNESS = Path(__file__).resolve().parent
ROOT = HARNESS.parent
CONFIG_PATH = HARNESS / "config.json"  # personal (identity), gitignored; created by `init`
EXAMPLE_CONFIG_PATH = HARNESS / "config.example.json"
# Until `init` has written config.json, read the example so the module-level settings below resolve;
# main() refuses to run any stage without the real file.
CONFIG = json.loads((CONFIG_PATH if CONFIG_PATH.exists() else EXAMPLE_CONFIG_PATH).read_text())
ORG = CONFIG["org"]
LOGIN = CONFIG["github_login"]
RAW = ROOT / "data" / "raw"
CACHE = ROOT / "cache"
MIRRORS = CACHE / "mirrors"

# Never prompt for credentials; route github.com auth through the gh CLI regardless of
# what helper (possibly a stale store path) the user's global git config points at.
GIT_ENV = {
    **os.environ,
    "GIT_TERMINAL_PROMPT": "0",
    "GIT_CONFIG_COUNT": "2",
    "GIT_CONFIG_KEY_0": "credential.https://github.com.helper",
    "GIT_CONFIG_VALUE_0": "",
    "GIT_CONFIG_KEY_1": "credential.https://github.com.helper",
    "GIT_CONFIG_VALUE_1": "!gh auth git-credential",
}

RETRYABLE = re.compile(r"rate limit|secondary|abuse|timeout|timed out|EOF|reset|50[234]|something went wrong", re.I)


def log(msg):
    print(f"[{dt.datetime.now():%H:%M:%S}] {msg}", file=sys.stderr, flush=True)


def now_iso():
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")


# --------------------------------------------------------------------------- io helpers

def write_json(path, obj):
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(json.dumps(obj, indent=2, ensure_ascii=False, default=str) + "\n")
    tmp.replace(path)


def write_jsonl(path, rows):
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    with tmp.open("w") as f:
        for row in rows:
            f.write(json.dumps(row, ensure_ascii=False, default=str) + "\n")
    tmp.replace(path)


def read_json(path, default=None):
    return json.loads(path.read_text()) if path.exists() else default


def read_jsonl(path):
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]


def write_text(base, relpath, text):
    """Write repo-controlled relpath under base, refusing anything that would escape it."""
    if relpath.startswith("/") or ".." in relpath.split("/"):
        log(f"refusing to write suspicious path {relpath!r} under {base}")
        return
    path = base / relpath
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def selected(repos, args):
    if not args.only:
        return repos
    wanted = set(args.only.split(","))
    return [r for r in repos if r["name"] in wanted]


def load_repos():
    repos = read_json(RAW / "org" / "repos.json")
    if repos is None:
        sys.exit("run the inventory stage first")
    return repos


# --------------------------------------------------------------------------- gh helpers

def gh(args, retries=6):
    for attempt in range(retries):
        p = subprocess.run(["gh", *args], capture_output=True, text=True)
        if p.returncode == 0:
            return p.stdout
        # gh exits non-zero on GraphQL errors but may still print usable data
        if p.stdout.strip().startswith("{") and '"data"' in p.stdout and not RETRYABLE.search(p.stderr):
            return p.stdout
        if attempt < retries - 1 and RETRYABLE.search(p.stderr):
            wait = min(60 * (attempt + 1), 300)
            log(f"gh transient error, retrying in {wait}s: {(p.stderr or p.stdout).strip()[:200]}")
            time.sleep(wait)
            continue
        raise RuntimeError(f"gh {' '.join(args[:2])} failed: {(p.stderr or p.stdout).strip()[:800]}")


def graphql(query, **variables):
    args = ["api", "graphql", "-f", f"query={query}"]
    for key, value in variables.items():
        if value is None:
            continue
        if isinstance(value, bool) or isinstance(value, int):
            args += ["-F", f"{key}={json.dumps(value)}"]
        else:
            args += ["-f", f"{key}={value}"]
    out = json.loads(gh(args))
    if out.get("errors"):
        log(f"graphql errors: {json.dumps(out['errors'])[:500]}")
        if not out.get("data"):
            raise RuntimeError("graphql query failed")
    return out["data"]


# --------------------------------------------------------------------------- git helpers

def git(repo, *args, input=None, check=True):
    p = subprocess.run(["git", "--git-dir", str(repo), *args], input=input, capture_output=True, env=GIT_ENV)
    if check and p.returncode != 0:
        raise RuntimeError(f"git {' '.join(args[:3])} failed in {repo.name}: {p.stderr.decode(errors='replace')[:500]}")
    return p.stdout


def mirror_path(name):
    return MIRRORS / f"{name}.git"


def mirrored(repos):
    return [r for r in repos if (mirror_path(r["name"]) / "HEAD").exists()]


def has_ref(repo, ref):
    return git(repo, "rev-parse", "--verify", "--quiet", ref, check=False).strip() != b""


# --------------------------------------------------------------------------- identity + text helpers

ID_EMAILS = {e.lower() for e in CONFIG["identity"]["emails"]}
ID_NAMES = {n.lower() for n in CONFIG["identity"]["names"]}
NOREPLY = re.compile(rf"^(\d+\+)?{re.escape(LOGIN)}@users\.noreply\.github\.com$", re.I)
DISCOVERY = re.compile(CONFIG["identity"]["discovery_pattern"], re.I)
COAUTHOR_RE = re.compile(r"^\s*co-authored-by:\s*(.+?)\s*$", re.I | re.M)
# Jira project keys: the org's Jira projects (config jira.projects_file, a JSON list of {"key": ...})
# plus archived/hidden ones seen in branch names (config jira.extra_project_keys). With neither,
# jira_keys stays empty: a bare KEY-123 regex matches too much (UTF-8, ISO-8601, dotnet-8...).
JIRA_FILE = HARNESS / CONFIG["jira"]["projects_file"] if CONFIG["jira"].get("projects_file") else None
JIRA_PROJECTS = json.loads(JIRA_FILE.read_text()) if JIRA_FILE and JIRA_FILE.exists() else []
JIRA_KEYS = {p["key"].upper() for p in JIRA_PROJECTS} | {k.upper() for k in CONFIG["jira"]["extra_project_keys"]}
# Jira issue numbers never have leading zeros, which also rules out analyzer IDs like ACME0901
JIRA_RE = re.compile(r"(?<![A-Za-z0-9])([A-Za-z][A-Za-z0-9]{1,9})-([1-9]\d{0,5})(?![0-9])")
JIRA_NO_DASH_RE = re.compile(r"(?<![A-Za-z0-9])([A-Za-z]{4,10})([1-9]\d{2,5})(?![0-9])")  # "SHOP3481" typos
GUID_RE = re.compile(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")
GITHUB_PR_IN_MESSAGE = [
    re.compile(r"\(#(\d+)\)\s*$", re.M),                    # squash merge: "Title (#123)"
    re.compile(r"^Merge pull request #(\d+)\b", re.M),      # merge commit
]
ADO_PR_IN_MESSAGE = re.compile(r"^Merged PR (\d+):", re.M)  # Azure DevOps-era (pre-GitHub) PR merges


def identity_match(name, email):
    """How a (name, email) pair matches the configured identity: 'email', 'name' or None."""
    email = (email or "").strip().lower()
    if email in ID_EMAILS or NOREPLY.match(email):
        return "email"
    if (name or "").strip().lower() in ID_NAMES:
        return "name"
    return None


def parse_coauthors(message):
    people = []
    for value in COAUTHOR_RE.findall(message):
        m = re.match(r"^(.*?)\s*<([^>]*)>$", value)
        people.append({"name": m.group(1).strip(), "email": m.group(2).strip()} if m else {"name": value, "email": ""})
    return people


def jira_keys(*texts):
    """Jira keys (PROJ-123, case-insensitive) for known Jira projects in the given texts."""
    keys = set()
    for text in texts:
        text = GUID_RE.sub(" ", text or "")
        for project, number in JIRA_RE.findall(text) + JIRA_NO_DASH_RE.findall(text):
            if project.upper() in JIRA_KEYS:
                keys.add(f"{project.upper()}-{number}")
    return sorted(keys)


def pr_numbers_in_message(message):
    return sorted({int(n) for rx in GITHUB_PR_IN_MESSAGE for n in rx.findall(message)})


def ado_pr_numbers_in_message(message):
    return sorted({int(n) for n in ADO_PR_IN_MESSAGE.findall(message)})


# --------------------------------------------------------------------------- stage: inventory

REPOS_QUERY = """
query($org: String!, $cursor: String) {
  organization(login: $org) {
    repositories(first: 50, after: $cursor, orderBy: {field: NAME, direction: ASC}) {
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes {
        name nameWithOwner url description homepageUrl
        isArchived isEmpty isFork isPrivate isTemplate isMirror visibility
        createdAt updatedAt pushedAt archivedAt diskUsage
        defaultBranchRef { name }
        primaryLanguage { name }
        languages(first: 25, orderBy: {field: SIZE, direction: DESC}) { totalSize edges { size node { name } } }
        repositoryTopics(first: 50) { nodes { topic { name } } }
        parent { nameWithOwner }
        templateRepository { nameWithOwner }
      }
    }
  }
}
"""


def stage_inventory(args):
    repos, cursor = [], None
    while True:
        data = graphql(REPOS_QUERY, org=ORG, cursor=cursor)["organization"]["repositories"]
        for n in data["nodes"]:
            repos.append({
                "name": n["name"],
                "full_name": n["nameWithOwner"],
                "url": n["url"],
                "description": n["description"],
                "homepage": n["homepageUrl"] or None,
                "default_branch": (n["defaultBranchRef"] or {}).get("name"),
                "visibility": n["visibility"],
                "is_archived": n["isArchived"],
                "is_empty": n["isEmpty"],
                "is_fork": n["isFork"],
                "is_template": n["isTemplate"],
                "is_mirror": n["isMirror"],
                "fork_of": (n["parent"] or {}).get("nameWithOwner"),
                "generated_from_template": (n["templateRepository"] or {}).get("nameWithOwner"),
                "created_at": n["createdAt"],
                "updated_at": n["updatedAt"],
                "pushed_at": n["pushedAt"],
                "archived_at": n["archivedAt"],
                "disk_usage_kb": n["diskUsage"],
                "primary_language": (n["primaryLanguage"] or {}).get("name"),
                "languages": [{"name": e["node"]["name"], "bytes": e["size"]} for e in n["languages"]["edges"]],
                "topics": [t["topic"]["name"] for t in n["repositoryTopics"]["nodes"]],
            })
        log(f"inventory: {len(repos)}/{data['totalCount']} repos")
        if not data["pageInfo"]["hasNextPage"]:
            break
        cursor = data["pageInfo"]["endCursor"]
    repos.sort(key=lambda r: r["name"].lower())
    write_json(RAW / "org" / "repos.json", repos)
    log(f"inventory: wrote {len(repos)} repos")


# --------------------------------------------------------------------------- stage: mirror

def mirror_one(repo):
    name = repo["name"]
    path = mirror_path(name)
    started = time.time()
    try:
        if not (path / "HEAD").exists():
            if path.exists():
                shutil.rmtree(path)  # half-initialised from an interrupted run
            path.mkdir(parents=True)
            git(path, "init", "--bare", "--quiet")
            git(path, "remote", "add", "origin", f"https://github.com/{repo['full_name']}.git")
            git(path, "config", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
            git(path, "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*")
            if CONFIG["mirror"].get("fetch_pull_refs", True):
                git(path, "config", "--add", "remote.origin.fetch", "+refs/pull/*/head:refs/pull/*/head")
            git(path, "config", "gc.auto", "0")
        git(path, "fetch", "--prune", "--quiet", "origin")
        if repo["default_branch"]:
            git(path, "symbolic-ref", "HEAD", f"refs/heads/{repo['default_branch']}")
        refs = git(path, "for-each-ref", "--format=%(refname)").decode().split()
        size = sum(f.stat().st_size for f in path.rglob("*") if f.is_file())
        return {
            "name": name, "ok": True, "seconds": round(time.time() - started, 1), "size_bytes": size,
            "branches": sum(r.startswith("refs/heads/") for r in refs),
            "tags": sum(r.startswith("refs/tags/") for r in refs),
            "pull_heads": sum(r.startswith("refs/pull/") for r in refs),
            "fetched_at": now_iso(),
        }
    except Exception as e:  # keep going; failures are recorded and can be retried with --only
        return {"name": name, "ok": False, "seconds": round(time.time() - started, 1), "error": str(e)}


def stage_mirror(args):
    repos = selected([r for r in load_repos() if not r["is_empty"]], args)
    repos.sort(key=lambda r: -(r["disk_usage_kb"] or 0))  # biggest first for better packing
    status_path = CACHE / "mirror_status.json"
    status = read_json(status_path, {})
    with cf.ThreadPoolExecutor(CONFIG["mirror"]["parallelism"]) as pool:
        for i, result in enumerate(pool.map(mirror_one, repos), 1):
            status[result["name"]] = result
            mark = "ok " if result["ok"] else "ERR"
            log(f"mirror {i}/{len(repos)} {mark} {result['name']} ({result['seconds']}s) {result.get('error', '')[:200]}")
            write_json(status_path, status)
    failed = [r for r in status.values() if not r["ok"]]
    log(f"mirror: {len(status) - len(failed)} ok, {len(failed)} failed")


# --------------------------------------------------------------------------- stage: identities

def scan_identities(repo):
    path = mirror_path(repo["name"])
    found = Counter()
    out = git(path, "log", "--all", "--format=%an%x1f%ae%x1f%cn%x1f%ce").decode("utf-8", "replace")
    for line in filter(None, out.split("\n")):  # not splitlines(): that also splits on \x1c-\x1e, U+2028...
        an, ae, cn, ce = line.split("\x1f")
        for role, name, email in (("author", an, ae), ("committer", cn, ce)):
            if DISCOVERY.search(name) or DISCOVERY.search(email) or identity_match(name, email):
                found[(role, name, email)] += 1
    out = git(path, "log", "--all", "-i", "-E", f"--grep={DISCOVERY.pattern}", "--format=%B%x1e")
    for message in out.decode("utf-8", "replace").split("\x1e"):
        for person in parse_coauthors(message):
            name, email = person["name"], person["email"]
            if DISCOVERY.search(name) or DISCOVERY.search(email) or identity_match(name, email):
                found[("co-author", name, email)] += 1
    return repo["name"], found


def stage_identities(args):
    """Surface every identity variant so the configured identity list can be checked for gaps."""
    people = {}
    with cf.ThreadPoolExecutor(8) as pool:
        for name, found in pool.map(scan_identities, mirrored(load_repos())):
            for (role, pname, email), count in found.items():
                p = people.setdefault((pname, email.lower()), {
                    "name": pname, "email": email, "matched_by_config": identity_match(pname, email),
                    "author": 0, "committer": 0, "co-author": 0, "repos": set()})
                p[role] += count
                p["repos"].add(name)
    rows = sorted(people.values(), key=lambda p: -(p["author"] + p["committer"] + p["co-author"]))
    for p in rows:
        p["repos"] = sorted(p["repos"])
    write_json(RAW / "org" / "identities.json", {
        "generated_at": now_iso(),
        "configured": CONFIG["identity"],
        "note": "commit counts are per (name, email) pair across all refs of all mirrors; "
                "matched_by_config=null means the variant is NOT treated as the user",
        "identities": rows,
    })
    for p in rows:
        log(f"identity {p['matched_by_config'] or 'UNMATCHED':9} a={p['author']:<6} c={p['committer']:<6} "
            f"co={p['co-author']:<4} repos={len(p['repos']):<3} {p['name']} <{p['email']}>")


# --------------------------------------------------------------------------- stage: commits

DETAIL_FMT = "%x1e%H%x1f%P%x1f%an%x1f%ae%x1f%aI%x1f%cn%x1f%ce%x1f%cI%x1f%s%x1f%b%x1f%B%x1d"
MAX_FILES_PER_COMMIT = 5000
MAX_REFS_PER_KIND = 50


def candidate_roles(path):
    """Pass 1: sha -> {role: match_basis} for commits whose author/committer is the user,
    plus shas whose message mentions the user (possible Co-authored-by trailers)."""
    roles = {}
    out = git(path, "log", "--all", "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce").decode("utf-8", "replace")
    for line in filter(None, out.split("\n")):
        sha, an, ae, cn, ce = line.split("\x1f")
        matched = {}
        if basis := identity_match(an, ae):
            matched["author"] = basis
        if basis := identity_match(cn, ce):
            matched["committer"] = basis
        if matched:
            roles[sha] = matched
    mentions = git(path, "log", "--all", "-i", "-E", f"--grep={DISCOVERY.pattern}", "--format=%H").decode().split()
    return roles, set(mentions)


def parse_diff(section):
    """Parse `--raw --numstat -z` output for one commit into per-file records."""
    tokens = section.split("\0")
    files, numstat, i = [], {}, 0
    while i < len(tokens):
        tok = tokens[i].lstrip("\n")
        if not tok:
            i += 1
            continue
        if tok.startswith(":"):
            status = tok.split(" ")[-1]
            if status[0] in "RC":
                files.append({"path": tokens[i + 2], "old_path": tokens[i + 1], "status": status[0],
                              "similarity": int(status[1:] or 0)})
                i += 3
            else:
                files.append({"path": tokens[i + 1], "status": status[0]})
                i += 2
        else:
            added, deleted, rest = tok.split("\t", 2)
            if rest:
                numstat[rest] = (added, deleted)
                i += 1
            else:  # rename/copy: "added\tdeleted\t\0old\0new\0"
                numstat[tokens[i + 2]] = (added, deleted)
                i += 3
    for f in files:
        added, deleted = numstat.get(f["path"], ("-", "-"))
        f["binary"] = added == "-"
        f["additions"] = None if added == "-" else int(added)
        f["deletions"] = None if deleted == "-" else int(deleted)
    return files


def commit_details(path, shas):
    """Pass 2: full message, parents and per-file stats for the given commits."""
    out = git(path, "log", "--no-walk=unsorted", "--stdin", f"--format={DETAIL_FMT}",
              "--raw", "--numstat", "-z", "-M", "--diff-merges=off",
              input="\n".join(shas).encode()).decode("utf-8", "replace")
    details = {}
    for chunk in out.split("\x1e"):
        if not chunk.strip("\0\n"):
            continue
        header, _, diff = chunk.partition("\x1d")
        sha, parents, an, ae, ad, cn, ce, cd, subject, body, message = header.split("\x1f", 10)
        details[sha] = {
            "parents": parents.split(),
            "author": {"name": an, "email": ae, "date": ad},
            "committer": {"name": cn, "email": ce, "date": cd},
            "subject": subject,
            "body": body.strip(),
            "message": message.rstrip("\n"),
            "files": parse_diff(diff),
        }
    return details


def patch_ids(path, shas):
    """sha -> `git patch-id --stable`: identical for the same change rebased/cherry-picked elsewhere."""
    if not shas:
        return {}
    log_p = subprocess.Popen(["git", "--git-dir", str(path), "log", "--no-walk=unsorted", "--stdin", "-p",
                              "--format=commit %H", "--diff-merges=off", "--no-color", "--no-ext-diff",
                              "--no-textconv"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, env=GIT_ENV)
    pid_p = subprocess.Popen(["git", "--git-dir", str(path), "patch-id", "--stable"],
                             stdin=log_p.stdout, stdout=subprocess.PIPE, env=GIT_ENV)
    log_p.stdout.close()
    log_p.stdin.write("\n".join(shas).encode())  # log reads all of stdin before writing anything
    log_p.stdin.close()
    out, _ = pid_p.communicate()
    log_p.wait()
    return {commit: patch for patch, commit in (line.split() for line in out.decode().splitlines())}


def ref_containment(path, default_ref, targets):
    """sha -> refs (other than the default branch) whose history contains it, for commits that are
    not on the default branch. One topo-ordered pass pushing ref bitmasks from tips to ancestors."""
    if not targets:
        return {}
    ref_names, tips = [], defaultdict(int)
    for line in git(path, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(*objectname)").decode().splitlines():
        ref, obj, peeled = line.split("\0")
        if ref != default_ref:
            tips[peeled or obj] |= 1 << len(ref_names)
            ref_names.append(ref)
    exclude = ["--not", default_ref] if has_ref(path, default_ref) else []
    out = git(path, "rev-list", "--topo-order", "--parents", "--all", *exclude).decode()
    masks, result = {}, {}
    for line in out.splitlines():
        sha, *parents = line.split()
        mask = masks.pop(sha, 0) | tips.get(sha, 0)
        if sha in targets:
            result[sha] = [ref_names[i] for i, bit in enumerate(reversed(bin(mask)[2:])) if bit == "1"]
        for parent in parents:
            masks[parent] = masks.get(parent, 0) | mask
    return result


def classify_refs(refs):
    branches = sorted(r[len("refs/heads/"):] for r in refs if r.startswith("refs/heads/"))
    tags = sorted(r[len("refs/tags/"):] for r in refs if r.startswith("refs/tags/"))
    prs = sorted(int(r.split("/")[2]) for r in refs if r.startswith("refs/pull/"))
    return {
        "branches": branches[:MAX_REFS_PER_KIND],
        "pull_request_refs": prs[:MAX_REFS_PER_KIND],  # lowest number is usually the PR that introduced it
        "tags": tags[:MAX_REFS_PER_KIND],
        "containing_ref_counts": {"branches": len(branches), "pull_request_refs": len(prs), "tags": len(tags)},
    }


def existing_commits(path, shas):
    out = git(path, "cat-file", "--batch-check=%(objectname) %(objecttype)", input="\n".join(shas).encode())
    return {line.split()[0] for line in out.decode().splitlines() if line.endswith(" commit")}


def extract_commits(repo):
    name, path = repo["name"], mirror_path(repo["name"])
    roles, mentions = candidate_roles(path)
    in_my_prs = defaultdict(list)  # sha -> numbers of PRs authored by the user that contain it
    lands_my_prs = defaultdict(list)  # sha -> numbers of the user's PRs it squash-/merge-landed
    for pr in read_jsonl(RAW / "repos" / name / "prs.jsonl"):
        for c in pr["commits"]:
            in_my_prs[c["sha"]].append(pr["number"])
        if pr["merge_commit_sha"]:
            lands_my_prs[pr["merge_commit_sha"]].append(pr["number"])
    wanted = set(in_my_prs) | set(lands_my_prs)
    pr_shas = existing_commits(path, list(wanted)) if wanted else set()
    if len(pr_shas) < len(wanted):  # e.g. merged into a since-deleted branch, or history rewritten
        log(f"commits {name}: {len(wanted) - len(pr_shas)} PR commit(s) unreachable from any ref, skipped")
    candidates = sorted(set(roles) | mentions | pr_shas)
    if not candidates:
        return name, []
    details = commit_details(path, candidates)
    default_ref = f"refs/heads/{repo['default_branch']}"
    on_default = set(git(path, "rev-list", default_ref).decode().split()) if has_ref(path, default_ref) else set()

    rows = []
    for sha in candidates:
        d = details[sha]
        matched = dict(roles.get(sha, {}))
        co_authors = parse_coauthors(d["message"])
        for person in co_authors:
            if basis := identity_match(person["name"], person["email"]):
                matched.setdefault("co-author", basis)
        if sha in pr_shas:
            matched["pr-author"] = "github_pr"
        if not matched:  # message mentioned the user's name for some other reason
            continue
        files = d["files"]
        is_merge = len(d["parents"]) > 1
        rows.append({
            "repo": name,
            "sha": sha,
            "url": f"https://github.com/{repo['full_name']}/commit/{sha}",
            "roles": [r for r in ("author", "committer", "co-author", "pr-author") if r in matched],
            "matched_by": matched,
            "in_user_prs": sorted(in_my_prs.get(sha, [])),
            "merge_commit_for_user_prs": sorted(lands_my_prs.get(sha, [])),
            "author": d["author"],
            "committer": d["committer"],
            "co_authors": co_authors,
            "subject": d["subject"],
            "body": d["body"],
            "message": d["message"],
            "parents": d["parents"],
            "is_merge": is_merge,
            "on_default_branch": sha in on_default,
            "pr_numbers_in_message": pr_numbers_in_message(d["message"]),
            "azure_devops_pr_numbers_in_message": ado_pr_numbers_in_message(d["message"]),
            "jira_keys": jira_keys(d["message"]),
            "stats": None if is_merge else {
                "files": len(files),
                "additions": sum(f["additions"] or 0 for f in files),
                "deletions": sum(f["deletions"] or 0 for f in files),
                "binary_files": sum(f["binary"] for f in files),
            },
            "files": files[:MAX_FILES_PER_COMMIT],
            "files_truncated": len(files) > MAX_FILES_PER_COMMIT,
        })

    containment = ref_containment(path, default_ref, {r["sha"] for r in rows if not r["on_default_branch"]})
    patches = patch_ids(path, [r["sha"] for r in rows if not r["is_merge"]])
    for row in rows:
        row["patch_id"] = patches.get(row["sha"])
        refs = classify_refs(containment.get(row["sha"], []))
        row.update(refs)
        row["jira_keys"] = sorted(set(row["jira_keys"]) | set(jira_keys(" ".join(refs["branches"]))))
    rows.sort(key=lambda r: (utc(r["author"]["date"]), r["sha"]))
    return name, rows


def stage_commits(args):
    repos = selected(mirrored(load_repos()), args)
    if not JIRA_KEYS:
        log("commits: no Jira project keys configured (config jira.*), so jira_keys will be empty")
    totals = Counter()
    with cf.ThreadPoolExecutor(6) as pool:
        futures = {pool.submit(extract_commits, r): r["name"] for r in repos}
        for i, future in enumerate(cf.as_completed(futures), 1):
            name, rows = future.result()
            out = RAW / "repos" / name / "commits.jsonl"
            if rows:
                write_jsonl(out, rows)
            elif out.exists():
                out.unlink()
            totals["commits"] += len(rows)
            totals["repos"] += bool(rows)
            if rows:
                log(f"commits {i}/{len(repos)} {name}: {len(rows)}")
    log(f"commits: {totals['commits']} commits in {totals['repos']} repos")


# --------------------------------------------------------------------------- stage: catalog

ENTITY_GREP = r"^apiVersion:[[:space:]]*['\"]?(backstage\.io|scaffolder\.backstage\.io)/"
CATALOG_FILENAMES = {"catalog-info.yaml", "catalog-info.yml"}


def entity_ref(kind, metadata):
    metadata = metadata if isinstance(metadata, dict) else {}
    namespace = str(metadata.get("namespace") or "default").lower()
    return f"{str(kind).lower()}:{namespace}/{str(metadata.get('name', '')).lower()}"


def repos_in_text(text, names_lower):
    found = []
    for m in re.finditer(rf"github\.com[/:]{re.escape(ORG)}/([A-Za-z0-9._-]+)", str(text or ""), re.I):
        name = names_lower.get(m.group(1).removesuffix(".git").lower())
        if name and name not in found:
            found.append(name)
    return found


def entity_repo_signals(entity, names_lower):
    meta = entity.get("metadata") if isinstance(entity.get("metadata"), dict) else {}
    annotations = meta.get("annotations") if isinstance(meta.get("annotations"), dict) else {}
    slug = str(annotations.get("github.com/project-slug") or "")
    slug_repo = None
    if slug.lower().startswith(ORG.lower() + "/"):
        slug_repo = names_lower.get(slug.split("/", 1)[1].lower())
    location_repos = repos_in_text(annotations.get("backstage.io/source-location"), names_lower)
    link_repos = []
    for link in meta.get("links") or []:
        if isinstance(link, dict):
            link_repos += [r for r in repos_in_text(link.get("url"), names_lower) if r not in link_repos]
    return {"slug": slug_repo, "source_location": location_repos[0] if location_repos else None, "links": link_repos}


def resolve_source_repo(signals, found_in, hubs):
    """Which repo an entity describes: explicit annotations first, then the repo the file lives in,
    except for catalog hub repos (which hold entities for other repos) where links are used."""
    if signals["slug"]:
        return signals["slug"], "annotation:github.com/project-slug"
    if signals["source_location"]:
        return signals["source_location"], "annotation:backstage.io/source-location"
    if found_in not in hubs:
        return found_in, "file_location"
    if signals["links"]:
        return signals["links"][0], "metadata.links"
    return None, None


def scan_catalog(repo):
    path, ref = mirror_path(repo["name"]), f"refs/heads/{repo['default_branch']}"
    if not has_ref(path, ref):
        return repo["name"], [], []
    tree = git(path, "ls-tree", "-r", "-z", "--name-only", ref).decode("utf-8", "replace").split("\0")
    files = {p for p in tree if p and p.rsplit("/", 1)[-1].lower() in CATALOG_FILENAMES}
    out = git(path, "grep", "-l", "-z", "-I", "-E", ENTITY_GREP, ref, "--", "*.yaml", "*.yml", check=False)
    files |= {item.split(":", 1)[1] for item in out.decode("utf-8", "replace").split("\0") if ":" in item}
    docs, errors = [], []
    for file in sorted(files):
        text = git(path, "cat-file", "blob", f"{ref}:{file}").decode("utf-8", "replace")
        write_text(RAW / "backstage" / "files" / repo["name"], file, text)
        try:
            parsed = [d for d in yaml.safe_load_all(text) if isinstance(d, dict)]
        except yaml.YAMLError as e:
            errors.append({"repo": repo["name"], "path": file, "error": str(e)[:500]})
            continue
        for i, doc in enumerate(parsed):
            if "kind" in doc and str(doc.get("apiVersion", "")).find("backstage.io") >= 0:
                docs.append((file, i, doc))
    return repo["name"], docs, errors


def stage_catalog(args):
    repos = mirrored(load_repos())
    names_lower = {r["name"].lower(): r["name"] for r in repos}
    hubs = set(CONFIG.get("catalog_hub_repos", []))
    shutil.rmtree(RAW / "backstage" / "files", ignore_errors=True)
    entities, errors = [], []
    with cf.ThreadPoolExecutor(8) as pool:
        for name, docs, errs in pool.map(scan_catalog, repos):
            errors += errs
            for file, index, doc in docs:
                signals = entity_repo_signals(doc, names_lower)
                source, basis = resolve_source_repo(signals, name, hubs)
                entities.append({
                    "ref": entity_ref(doc.get("kind"), doc.get("metadata")),
                    "kind": doc.get("kind"),
                    "found_in_repo": name,
                    "path": file,
                    "doc_index": index,
                    "is_template_skeleton": "/skeleton/" in f"/{file}",
                    "source_repo": source,
                    "source_repo_basis": basis,
                    "repo_signals": signals,
                    "entity": doc,
                })
    entities.sort(key=lambda e: (e["found_in_repo"], e["path"], e["doc_index"]))
    write_jsonl(RAW / "backstage" / "entities.jsonl", entities)
    write_json(RAW / "backstage" / "parse_errors.json", errors)
    kinds = Counter(e["kind"] for e in entities)
    log(f"catalog: {len(entities)} entities ({dict(kinds)}), {len(errors)} unparseable files")


# --------------------------------------------------------------------------- stage: prs

PR_COMMIT_FIELDS = "commit { oid messageHeadline authoredDate author { name email user { login } } }"
PR_FIELDS = f"""
  number title body url state isDraft createdAt updatedAt closedAt mergedAt
  repository {{ name }}
  author {{ login }}
  mergedBy {{ login }}
  baseRefName headRefName headRefOid
  mergeCommit {{ oid }}
  additions deletions changedFiles reviewDecision
  labels(first: 30) {{ nodes {{ name }} }}
  assignees(first: 10) {{ nodes {{ login }} }}
  comments {{ totalCount }}
  reviews(first: 100) {{ totalCount nodes {{ author {{ login }} state submittedAt }} }}
  reviewRequests(first: 20) {{ nodes {{ requestedReviewer {{ ... on User {{ login }} ... on Team {{ name }} }} }} }}
  closingIssuesReferences(first: 20) {{ nodes {{ number title url repository {{ nameWithOwner }} }} }}
  commits(first: 100) {{ totalCount nodes {{ {PR_COMMIT_FIELDS} }} }}
  files(first: 100) {{ totalCount nodes {{ path additions deletions changeType }} }}
"""
SEARCH_COUNT_QUERY = "query($q: String!) { search(query: $q, type: ISSUE, first: 1) { issueCount } }"
SEARCH_PRS_QUERY = f"""
query($q: String!, $cursor: String) {{
  search(query: $q, type: ISSUE, first: 25, after: $cursor) {{
    issueCount
    pageInfo {{ hasNextPage endCursor }}
    nodes {{ ... on PullRequest {{ {PR_FIELDS} }} }}
  }}
}}"""
PR_PAGE_QUERY = """
query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      %s(first: 100, after: $cursor) { pageInfo { hasNextPage endCursor } nodes { %s } }
    }
  }
}"""


def search_slices(base_query, start_year):
    """created: date ranges that each hold <= 1000 results (the GitHub search cap)."""
    pending = [(dt.date(start_year, 1, 1), dt.date.today() + dt.timedelta(days=1))]
    while pending:
        lo, hi = pending.pop(0)
        query = f"{base_query} created:{lo.isoformat()}..{hi.isoformat()}"
        count = graphql(SEARCH_COUNT_QUERY, q=query)["search"]["issueCount"]
        if count > 1000 and lo < hi:
            mid = lo + (hi - lo) // 2
            pending[:0] = [(lo, mid), (mid + dt.timedelta(days=1), hi)]
        elif count:
            yield query, count


def search_prs(base_query):
    for query, count in search_slices(base_query, CONFIG["search_start_year"]):
        cursor, got = None, 0
        while True:
            data = graphql(SEARCH_PRS_QUERY, q=query, cursor=cursor)["search"]
            nodes = [n for n in data["nodes"] if n]
            got += len(nodes)
            yield from nodes
            if not data["pageInfo"]["hasNextPage"]:
                break
            cursor = data["pageInfo"]["endCursor"]
        log(f"prs: {query.split(' created:')[1]} -> {got}/{count}")


def remaining_pages(pr, connection, fields, first_page):
    """Follow-up pagination for PR connections longer than the 100 fetched inline."""
    nodes = list(first_page["nodes"])
    if first_page["totalCount"] <= len(nodes):
        return nodes
    cursor, nodes = None, []
    while True:
        data = graphql(PR_PAGE_QUERY % (connection, fields), owner=ORG, name=pr["repository"]["name"],
                       number=pr["number"], cursor=cursor)
        page = data["repository"]["pullRequest"][connection]
        nodes += page["nodes"]
        if not page["pageInfo"]["hasNextPage"]:
            return nodes
        cursor = page["pageInfo"]["endCursor"]


def flatten_pr(pr):
    commits = remaining_pages(pr, "commits", PR_COMMIT_FIELDS, pr["commits"])
    files = remaining_pages(pr, "files", "path additions deletions changeType", pr["files"])
    login = lambda actor: (actor or {}).get("login")
    reviewer = lambda r: (r or {}).get("login") or (r or {}).get("name")
    return {
        "repo": pr["repository"]["name"],
        "number": pr["number"],
        "url": pr["url"],
        "title": pr["title"],
        "body": pr["body"],
        "state": pr["state"],
        "is_draft": pr["isDraft"],
        "created_at": pr["createdAt"],
        "updated_at": pr["updatedAt"],
        "closed_at": pr["closedAt"],
        "merged_at": pr["mergedAt"],
        "author": login(pr["author"]),
        "merged_by": login(pr["mergedBy"]),
        "base_ref": pr["baseRefName"],
        "head_ref": pr["headRefName"],
        "head_sha": pr["headRefOid"],
        "merge_commit_sha": (pr["mergeCommit"] or {}).get("oid"),
        "additions": pr["additions"],
        "deletions": pr["deletions"],
        "changed_files": pr["changedFiles"],
        "review_decision": pr["reviewDecision"],
        "labels": [n["name"] for n in pr["labels"]["nodes"]],
        "assignees": [n["login"] for n in pr["assignees"]["nodes"]],
        "comment_count": pr["comments"]["totalCount"],
        "review_count": pr["reviews"]["totalCount"],
        "reviews": [{"author": login(r["author"]), "state": r["state"], "submitted_at": r["submittedAt"]}
                    for r in pr["reviews"]["nodes"]],
        "requested_reviewers": [reviewer(n["requestedReviewer"]) for n in pr["reviewRequests"]["nodes"]],
        "closing_issues": [{"repo": n["repository"]["nameWithOwner"], "number": n["number"], "title": n["title"],
                            "url": n["url"]} for n in pr["closingIssuesReferences"]["nodes"]],
        "jira_keys": jira_keys(pr["title"], pr["body"], pr["headRefName"]),
        "commit_count": pr["commits"]["totalCount"],
        "commits": [{"sha": c["commit"]["oid"], "headline": c["commit"]["messageHeadline"],
                     "authored_at": c["commit"]["authoredDate"],
                     "author_name": (c["commit"]["author"] or {}).get("name"),
                     "author_email": (c["commit"]["author"] or {}).get("email"),
                     "author_login": login((c["commit"]["author"] or {}).get("user"))} for c in commits],
        "file_count": pr["files"]["totalCount"],
        "files": [{"path": f["path"], "additions": f["additions"], "deletions": f["deletions"],
                   "change_type": f["changeType"]} for f in files],
    }


def stage_prs(args):
    by_repo = defaultdict(dict)
    for pr in search_prs(f"is:pr author:{LOGIN} org:{ORG}"):
        by_repo[pr["repository"]["name"]][pr["number"]] = flatten_pr(pr)
    for old in (RAW / "repos").glob("*/prs.jsonl"):
        if old.parent.name not in by_repo:
            old.unlink()
    for repo, prs in by_repo.items():
        write_jsonl(RAW / "repos" / repo / "prs.jsonl", [prs[n] for n in sorted(prs)])
    log(f"prs: {sum(map(len, by_repo.values()))} PRs in {len(by_repo)} repos")


# --------------------------------------------------------------------------- stage: projects

ROOT_DOCS = re.compile(r"^(readme(\.\w+)?|claude\.md|agents\.md|\.github/copilot-instructions\.md|"
                       r"docs/(index|readme)\.md|mkdocs\.ya?ml|package\.json)$", re.I)
README_RE = re.compile(r"^readme(\.\w+)?$", re.I)
MAX_DOC_BYTES = 200_000
TREE_TOP_LIMIT = 400


def utc(ts):
    return dt.datetime.fromisoformat(ts.replace("Z", "+00:00")).astimezone(dt.timezone.utc) if ts else None


def iso_utc(ts):
    return utc(ts).isoformat() if ts else None


def project_docs(repo, catalog_dirs):
    """Root README/CLAUDE.md/AGENTS.md/docs index/package.json, plus READMEs next to catalog-info files."""
    path, ref = mirror_path(repo["name"]), f"refs/heads/{repo['default_branch']}"
    if not has_ref(path, ref):
        return [], []
    tree = [p for p in git(path, "ls-tree", "-r", "-z", "--name-only", ref).decode("utf-8", "replace").split("\0") if p]
    out_dir = RAW / "repos" / repo["name"] / "docs"
    shutil.rmtree(out_dir, ignore_errors=True)
    docs = []
    for file in tree:
        directory, _, base = file.rpartition("/")
        if not (ROOT_DOCS.match(file) or (directory in catalog_dirs and directory and README_RE.match(base))):
            continue
        blob = git(path, "cat-file", "blob", f"{ref}:{file}")
        write_text(out_dir, file, blob[:MAX_DOC_BYTES].decode("utf-8", "replace"))
        docs.append({"path": file, "saved_as": f"docs/{file}", "bytes": len(blob), "truncated": len(blob) > MAX_DOC_BYTES})
    # directory skeleton (depth <= 2) as a fallback for undocumented repos
    top = set()
    for file in tree:
        parts = file.split("/")
        top.add(parts[0] + ("/" if len(parts) > 1 else ""))
        if len(parts) > 2:
            top.add(f"{parts[0]}/{parts[1]}/")
    return docs, sorted(top)[:TREE_TOP_LIMIT]


def history_info(repo):
    path = mirror_path(repo["name"])
    roots = git(path, "log", "--all", "--max-parents=0", "--format=%aI").decode().split()
    status = read_json(CACHE / "mirror_status.json", {}).get(repo["name"], {})
    return {
        "first_commit_date": min(roots, key=utc) if roots else None,
        "root_commits": len(roots),
        "commits_all_refs": int(git(path, "rev-list", "--all", "--count").decode().strip() or 0),
        "branches": status.get("branches"),
        "tags": status.get("tags"),
        "pull_request_refs": status.get("pull_heads"),
        "mirrored_at": status.get("fetched_at"),
    }


def spec_refs(entity):
    """Entity refs this entity points at that describe *what it is part of* (system, domain, owner, parent)."""
    spec = entity.get("spec") if isinstance(entity.get("spec"), dict) else {}
    refs = []
    for field, default_kind in (("system", "system"), ("domain", "domain"), ("owner", "group"),
                                ("subcomponentOf", "component"), ("parent", "group")):
        value = spec.get(field)
        if isinstance(value, str) and value:
            kind, _, rest = value.rpartition(":") if ":" in value else (default_kind, "", value)
            namespace, _, name = rest.rpartition("/") if "/" in rest else ("default", "", rest)
            refs.append(f"{kind.lower()}:{namespace.lower()}/{name.lower()}")
    return refs


def summarize_activity(commits, prs):
    authored = [c for c in commits if "author" in c["roles"]]
    authored_dates = sorted((c["author"]["date"] for c in authored), key=utc)
    real_work = [c for c in authored if not c["is_merge"] and c["stats"]]
    pr_dates = sorted((p["created_at"] for p in prs), key=utc)
    return {
        "commits_total": len(commits),
        "commits_by_roles": dict(Counter("+".join(c["roles"]) for c in commits).most_common()),
        "authored": len(authored),
        "authored_on_default_branch": sum(c["on_default_branch"] for c in authored),
        "authored_only_on_other_refs": sum(not c["on_default_branch"] for c in authored),
        "authored_merges": sum(c["is_merge"] for c in authored),
        "authored_additions": sum(c["stats"]["additions"] for c in real_work),
        "authored_deletions": sum(c["stats"]["deletions"] for c in real_work),
        "first_authored_at": iso_utc(authored_dates[0]) if authored_dates else None,
        "last_authored_at": iso_utc(authored_dates[-1]) if authored_dates else None,
        "jira_keys": len({k for c in commits for k in c["jira_keys"]} | {k for p in prs for k in p["jira_keys"]}),
        "prs_authored": len(prs),
        "prs_merged": sum(bool(p["merged_at"]) for p in prs),
        "first_pr_at": pr_dates[0] if pr_dates else None,
        "last_pr_at": pr_dates[-1] if pr_dates else None,
    }


def stage_projects(args):
    repos = {r["name"]: r for r in load_repos()}
    entities = [e for e in read_jsonl(RAW / "backstage" / "entities.jsonl") if not e["is_template_skeleton"]]
    by_ref = {}
    for e in entities:
        by_ref.setdefault(e["ref"], e)
    active = []
    for d in sorted((RAW / "repos").iterdir()):
        if (d / "commits.jsonl").exists() or (d / "prs.jsonl").exists():
            active.append(d.name)
        elif d.is_dir():
            shutil.rmtree(d)  # no longer has any activity (e.g. identity config changed)
    index = []
    for name in active:
        repo = repos.get(name)
        if not repo:
            log(f"projects: {name} not in inventory (renamed/removed?), skipping")
            continue
        kind_order = {"System": 0, "Component": 1, "API": 2, "Resource": 3}
        own = sorted((e for e in entities if e["source_repo"] == name),  # systems and repo-root entities first
                     key=lambda e: (kind_order.get(e["kind"], 9), e["path"].count("/"), e["path"], e["doc_index"]))
        context, queue = {}, [ref for e in own for ref in spec_refs(e["entity"])]
        while queue:  # follow system -> domain etc.
            ref = queue.pop()
            if ref in context or any(e["ref"] == ref for e in own):
                continue
            target = by_ref.get(ref)
            context[ref] = None if target is None else {k: target[k] for k in ("kind", "found_in_repo", "path", "entity")}
            if target:
                queue += spec_refs(target["entity"])
        catalog_dirs = {e["path"].rpartition("/")[0] for e in own if e["found_in_repo"] == name}
        docs, tree_top = project_docs(repo, catalog_dirs) if (mirror_path(name) / "HEAD").exists() else ([], [])
        commits = read_jsonl(RAW / "repos" / name / "commits.jsonl")
        prs = read_jsonl(RAW / "repos" / name / "prs.jsonl")
        activity = summarize_activity(commits, prs)
        history = history_info(repo) if (mirror_path(name) / "HEAD").exists() else None
        write_json(RAW / "repos" / name / "project.json", {
            "name": name,
            "url": repo["url"],
            "github": repo,
            "history": history,
            "backstage": {
                "entities": [{k: e[k] for k in ("ref", "kind", "found_in_repo", "path", "source_repo_basis", "entity")}
                             for e in own],
                "context": context,
            },
            "docs": docs,
            "tree_top": tree_top,
            "activity": activity,
        })
        index.append({
            "name": name,
            "url": repo["url"],
            "dir": f"repos/{name}",
            "files": {kind: f"repos/{name}/{kind}.jsonl" for kind in ("commits", "prs")
                      if (RAW / "repos" / name / f"{kind}.jsonl").exists()},
            "archived": repo["is_archived"],
            "github_description": repo["description"],
            "backstage": [{"ref": e["ref"], "title": (e["entity"].get("metadata") or {}).get("title"),
                           "description": (e["entity"].get("metadata") or {}).get("description"),
                           "type": (e["entity"].get("spec") or {}).get("type")}
                          for e in own if e["kind"] in ("Component", "System", "API", "Resource")],
            "docs": [d["saved_as"] for d in docs],
            "first_commit_in_repo": history and iso_utc(history["first_commit_date"]),
            "github_created_at": repo["created_at"],
            **activity,
        })
        log(f"projects: {name}: {len(own)} entities, {len(docs)} docs, {activity['commits_total']} commits, "
            f"{activity['prs_authored']} prs")
    index.sort(key=lambda r: -(r["authored"] + r["prs_authored"]))
    all_commits = [c for r in index for c in read_jsonl(RAW / r["dir"] / "commits.jsonl")]
    write_json(RAW / "index.json", {
        "generated_at": now_iso(),
        "org": ORG,
        "github_login": LOGIN,
        "identity": CONFIG["identity"],
        "schema": "README.md",
        "totals": {
            "repos": len(index),
            "commits": len(all_commits),
            "authored_commits": sum(r["authored"] for r in index),
            "authored_on_default_branch": sum(r["authored_on_default_branch"] for r in index),
            "prs_authored": sum(r["prs_authored"] for r in index),
            "first_authored_at": min((r["first_authored_at"] for r in index if r["first_authored_at"]), default=None),
            "last_authored_at": max((r["last_authored_at"] for r in index if r["last_authored_at"]), default=None),
        },
        "repos": index,
    })
    if JIRA_PROJECTS:
        write_json(RAW / "org" / "jira_projects.json", JIRA_PROJECTS)
    shutil.copy(HARNESS / "DATA_README.md", RAW / "README.md")
    log(f"projects: indexed {len(index)} repos")


# --------------------------------------------------------------------------- stage: verify

def stage_verify(args):
    """Cross-check against GitHub's own attribution: every default-branch commit that commit search
    links to the account should be in the dataset (catches identity variants we don't know about)."""
    ours = {}
    for path in (RAW / "repos").glob("*/commits.jsonl"):
        for row in read_jsonl(path):
            ours[(row["repo"], row["sha"])] = row
    github, pending = {}, [(dt.date(2000, 1, 1), dt.date.today() + dt.timedelta(days=1))]
    while pending:
        lo, hi = pending.pop(0)
        q = f"author:{LOGIN} org:{ORG} author-date:{lo.isoformat()}..{hi.isoformat()}"
        page, items = 1, []
        while True:
            time.sleep(2.1)  # search API: 30 requests/minute
            data = json.loads(gh(["api", "-X", "GET", "search/commits", "-f", f"q={q}", "-f", "per_page=100",
                                  "-f", f"page={page}"]))
            if page == 1 and data["total_count"] > 1000 and lo < hi:
                mid = lo + (hi - lo) // 2
                pending[:0] = [(lo, mid), (mid + dt.timedelta(days=1), hi)]
                break
            items += data["items"]
            if len(data["items"]) < 100 or page == 10:
                for item in items:
                    github[(item["repository"]["name"], item["sha"])] = item
                log(f"verify: {lo}..{hi} -> {len(items)}/{data['total_count']}")
                break
            page += 1
    missing = [{"repo": repo, "sha": sha, "date": item["commit"]["author"]["date"],
                "author": f"{item['commit']['author']['name']} <{item['commit']['author']['email']}>",
                "subject": item["commit"]["message"].splitlines()[0] if item["commit"]["message"] else ""}
               for (repo, sha), item in github.items() if (repo, sha) not in ours]
    authored_default = sum(1 for r in ours.values() if "author" in r["roles"] and r["on_default_branch"])
    write_json(RAW / "org" / "verification.json", {
        "generated_at": now_iso(),
        "method": "GitHub commit search (default branches, commits GitHub links to the account) vs dataset",
        "github_search_commits": len(github),
        "dataset_commits": len(ours),
        "dataset_authored_on_default_branch": authored_default,
        "github_commits_missing_from_dataset": len(missing),
        "missing": sorted(missing, key=lambda m: m["date"]),
    })
    log(f"verify: GitHub links {len(github)} commits; {len(missing)} missing from dataset of {len(ours)}")


# --------------------------------------------------------------------------- init

def cmd_init(args):
    """Write harness/config.json for the gh-authenticated user. Emails come from the commits GitHub
    already attributes to the account in the org (no `user:email` token scope needed)."""
    if CONFIG_PATH.exists() and not args.force:
        sys.exit(f"{CONFIG_PATH} already exists; pass --force to overwrite it")
    if not args.org:
        sys.exit("init needs --org <github-org>")
    me = json.loads(gh(["api", "user"]))
    login, user_id = me["login"], me["id"]
    seen = Counter()
    for order in ("desc", "asc"):  # newest and oldest commits, where email changes show up
        for page in (1, 2, 3):
            time.sleep(2.1)  # search API: 30 requests/minute
            data = json.loads(gh(["api", "-X", "GET", "search/commits", "-f", f"q=author:{login} org:{args.org}",
                                  "-f", "sort=author-date", "-f", f"order={order}", "-f", "per_page=100",
                                  "-f", f"page={page}"]))
            for item in data["items"]:
                seen[(item["commit"]["author"]["name"], item["commit"]["author"]["email"].lower())] += 1
            if len(data["items"]) < 100:
                break
    emails = sorted({email for _, email in seen} | {f"{user_id}+{login}@users.noreply.github.com".lower()})
    names = {name.strip() for name, _ in seen if " " in name.strip()} | {login}
    if me.get("name") and " " in me["name"].strip():
        names.add(me["name"].strip())
    tokens = {t.lower() for n in names for t in re.split(r"\W+", n) if len(t) >= 3} | {login.lower()}

    config = json.loads(EXAMPLE_CONFIG_PATH.read_text())
    if args.org.lower() != config["org"].lower():  # the example's catalog/Jira defaults are for its org only
        config["catalog_hub_repos"] = []
        config["jira"] = {"projects_file": None, "extra_project_keys": []}
    config.update(org=args.org, github_login=login, github_user_id=user_id,
                  search_start_year=int(me["created_at"][:4]))
    config["identity"] = {"emails": emails, "names": sorted(names),
                          "discovery_pattern": "|".join(sorted(re.escape(t) for t in tokens))}
    write_json(CONFIG_PATH, config)
    log(f"init: wrote {CONFIG_PATH} for {login} in {args.org}")
    for (name, email), count in seen.most_common():
        log(f"init:   {count:>4} commits as {name} <{email}>")
    if not seen:
        log("init: GitHub attributes no commits in this org to the account; add your commit emails by hand")
    log("init: review identity.names (drop any you share with a colleague), then run `all`; the identities "
        "and verify stages report anything still missed")


# --------------------------------------------------------------------------- main

STAGES = {  # `all` runs these in order; commits needs prs (for the pr-author role)
    "inventory": stage_inventory,
    "mirror": stage_mirror,
    "identities": stage_identities,
    "prs": stage_prs,
    "commits": stage_commits,
    "catalog": stage_catalog,
    "projects": stage_projects,
    "verify": stage_verify,
}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("stages", nargs="+", choices=["init", *STAGES, "all"])
    parser.add_argument("--only", help="comma-separated repo names to limit a per-repo stage to")
    parser.add_argument("--org", help="init: the GitHub org to collect from")
    parser.add_argument("--force", action="store_true", help="init: overwrite an existing config.json")
    args = parser.parse_args()
    if "init" in args.stages:
        if len(args.stages) > 1:
            sys.exit("run init on its own, review harness/config.json, then run the stages")
        return cmd_init(args)
    if not CONFIG_PATH.exists():
        sys.exit("harness/config.json not found: run `python3 -I harness/scrape.py init --org <github-org>`")
    for name in (list(STAGES) if "all" in args.stages else args.stages):
        log(f"=== stage {name}")
        STAGES[name](args)


if __name__ == "__main__":
    main()
