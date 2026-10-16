package scrape

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/marleypowell/footprint"
)

var (
	rootDocsRE = regexp.MustCompile(`(?i)^(readme(\.\w+)?|claude\.md|agents\.md|\.github/copilot-instructions\.md|` +
		`docs/(index|readme)\.md|mkdocs\.ya?ml|package\.json)$`)
	readmeRE = regexp.MustCompile(`(?i)^readme(\.\w+)?$`)
)

const (
	maxDocBytes  = 200_000
	treeTopLimit = 400
)

// Project is <data>/repos/<repo>/project.json: what the repo is, plus the user's activity in it.
type Project struct {
	Name      string           `json:"name"`
	URL       string           `json:"url"`
	GitHub    Repo             `json:"github"`
	History   *History         `json:"history"`
	Backstage ProjectBackstage `json:"backstage"`
	Docs      []Doc            `json:"docs"`
	TreeTop   []string         `json:"tree_top"`
	Activity  Activity         `json:"activity"`
}

type History struct {
	FirstCommitDate *string `json:"first_commit_date"`
	RootCommits     int     `json:"root_commits"`
	CommitsAllRefs  int     `json:"commits_all_refs"`
	Branches        *int    `json:"branches"`
	Tags            *int    `json:"tags"`
	PullRequestRefs *int    `json:"pull_request_refs"`
	MirroredAt      *string `json:"mirrored_at"`
}

type ProjectBackstage struct {
	Entities []ProjectEntity `json:"entities"`
	// Context holds the entities those point at (system, domain, owner, parent), resolved
	// transitively; null when referenced but not defined in any YAML.
	Context map[string]*ContextEntity `json:"context"`
}

type ProjectEntity struct {
	Ref             string          `json:"ref"`
	Kind            any             `json:"kind"`
	FoundInRepo     string          `json:"found_in_repo"`
	Path            string          `json:"path"`
	SourceRepoBasis *string         `json:"source_repo_basis"`
	Entity          json.RawMessage `json:"entity"`
}

type ContextEntity struct {
	Kind        any             `json:"kind"`
	FoundInRepo string          `json:"found_in_repo"`
	Path        string          `json:"path"`
	Entity      json.RawMessage `json:"entity"`
}

type Doc struct {
	Path      string `json:"path"`
	SavedAs   string `json:"saved_as"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

// Activity is the headline counts for a repo, from its commits.jsonl and prs.jsonl.
type Activity struct {
	CommitsTotal            int         `json:"commits_total"`
	CommitsByRoles          *orderedMap `json:"commits_by_roles"` // most common first
	Authored                int         `json:"authored"`
	AuthoredOnDefaultBranch int         `json:"authored_on_default_branch"`
	AuthoredOnlyOnOtherRefs int         `json:"authored_only_on_other_refs"`
	AuthoredMerges          int         `json:"authored_merges"`
	AuthoredAdditions       int         `json:"authored_additions"`
	AuthoredDeletions       int         `json:"authored_deletions"`
	FirstAuthoredAt         *string     `json:"first_authored_at"`
	LastAuthoredAt          *string     `json:"last_authored_at"`
	JiraKeys                int         `json:"jira_keys"`
	PRsAuthored             int         `json:"prs_authored"`
	PRsMerged               int         `json:"prs_merged"`
	FirstPRAt               *string     `json:"first_pr_at"`
	LastPRAt                *string     `json:"last_pr_at"`
}

// Index is <data>/index.json, the entry point to the dataset.
type Index struct {
	GeneratedAt string         `json:"generated_at"`
	Org         string         `json:"org"`
	GitHubLogin string         `json:"github_login"`
	Identity    IdentityConfig `json:"identity"`
	Schema      string         `json:"schema"`
	Totals      IndexTotals    `json:"totals"`
	Repos       []IndexRepo    `json:"repos"`
}

type IndexTotals struct {
	Repos                   int     `json:"repos"`
	Commits                 int     `json:"commits"`
	AuthoredCommits         int     `json:"authored_commits"`
	AuthoredOnDefaultBranch int     `json:"authored_on_default_branch"`
	PRsAuthored             int     `json:"prs_authored"`
	FirstAuthoredAt         *string `json:"first_authored_at"`
	LastAuthoredAt          *string `json:"last_authored_at"`
}

type IndexRepo struct {
	Name              string            `json:"name"`
	URL               string            `json:"url"`
	Dir               string            `json:"dir"`
	Files             map[string]string `json:"files"`
	Archived          bool              `json:"archived"`
	GitHubDescription *string           `json:"github_description"`
	Backstage         []IndexEntity     `json:"backstage"`
	Docs              []string          `json:"docs"`
	FirstCommitInRepo *string           `json:"first_commit_in_repo"`
	GitHubCreatedAt   string            `json:"github_created_at"`
	Activity
}

type IndexEntity struct {
	Ref         string `json:"ref"`
	Title       any    `json:"title"`
	Description any    `json:"description"`
	Type        any    `json:"type"`
}

// projects writes each active repo's project.json and docs, then index.json and the schema doc.
func (h *harness) projects(ctx context.Context) error {
	repos, err := h.loadRepos()
	if err != nil {
		return err
	}
	byName := map[string]Repo{}
	for _, r := range repos {
		byName[r.Name] = r
	}
	stored, err := readJSONL[Entity](filepath.Join(h.raw, "backstage", "entities.jsonl"))
	if err != nil {
		return err
	}
	var entities []Entity
	docs := map[*Entity]any{} // each entity decoded once, for lookups
	byRef := map[string]*Entity{}
	for i := range stored {
		if stored[i].IsTemplateSkeleton {
			continue
		}
		entities = append(entities, stored[i])
	}
	for i := range entities {
		e := &entities[i]
		var doc any
		if err := json.Unmarshal(e.Entity, &doc); err != nil {
			return err
		}
		docs[e] = doc
		if _, seen := byRef[e.Ref]; !seen {
			byRef[e.Ref] = e
		}
	}
	statuses := map[string]MirrorStatus{}
	if _, err := readJSON(h.mirrorStatusPath(), &statuses); err != nil {
		return err
	}

	entries, err := os.ReadDir(filepath.Join(h.raw, "repos"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var index []IndexRepo
	for _, entry := range entries {
		name, dir := entry.Name(), filepath.Join(h.raw, "repos", entry.Name())
		hasCommits, hasPRs := fileExists(filepath.Join(dir, "commits.jsonl")), fileExists(filepath.Join(dir, "prs.jsonl"))
		if !hasCommits && !hasPRs {
			if entry.IsDir() { // no activity any more (identity config changed, say)
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
			}
			continue
		}
		repo, ok := byName[name]
		if !ok {
			logf("projects: %s not in inventory (renamed/removed?), skipping", name)
			continue
		}
		own := ownEntities(entities, name)
		related := resolveContext(own, byRef, docs)
		catalogDirs := map[string]bool{}
		for _, e := range own {
			if e.FoundInRepo == name {
				catalogDirs[parentDir(e.Path)] = true
			}
		}
		project := Project{Name: name, URL: repo.URL, GitHub: repo, Docs: []Doc{}, TreeTop: []string{},
			Backstage: ProjectBackstage{Entities: []ProjectEntity{}, Context: related}}
		if h.isMirrored(name) {
			if project.Docs, project.TreeTop, err = h.projectDocs(ctx, repo, catalogDirs); err != nil {
				return err
			}
			if project.History, err = h.historyInfo(ctx, repo, statuses[name]); err != nil {
				return err
			}
		}
		commits, err := readJSONL[Commit](filepath.Join(dir, "commits.jsonl"))
		if err != nil {
			return err
		}
		prs, err := readJSONL[PullRequest](filepath.Join(dir, "prs.jsonl"))
		if err != nil {
			return err
		}
		project.Activity = summarizeActivity(commits, prs)
		item := IndexRepo{
			Name: name, URL: repo.URL, Dir: "repos/" + name, Files: map[string]string{}, Archived: repo.IsArchived,
			GitHubDescription: repo.Description, Backstage: []IndexEntity{}, Docs: []string{},
			GitHubCreatedAt: repo.CreatedAt, Activity: project.Activity,
		}
		for _, e := range own {
			project.Backstage.Entities = append(project.Backstage.Entities, ProjectEntity{
				Ref: e.Ref, Kind: e.Kind, FoundInRepo: e.FoundInRepo, Path: e.Path, SourceRepoBasis: e.SourceRepoBasis, Entity: e.Entity,
			})
			if slices.Contains([]string{"Component", "System", "API", "Resource"}, asString(e.Kind)) {
				meta := field(docs[e], "metadata")
				item.Backstage = append(item.Backstage, IndexEntity{
					Ref: e.Ref, Title: field(meta, "title"), Description: field(meta, "description"), Type: field(field(docs[e], "spec"), "type"),
				})
			}
		}
		if hasCommits {
			item.Files["commits"] = "repos/" + name + "/commits.jsonl"
		}
		if hasPRs {
			item.Files["prs"] = "repos/" + name + "/prs.jsonl"
		}
		for _, d := range project.Docs {
			item.Docs = append(item.Docs, d.SavedAs)
		}
		if project.History != nil && project.History.FirstCommitDate != nil {
			item.FirstCommitInRepo = isoUTC(*project.History.FirstCommitDate)
		}
		if err := writeJSON(filepath.Join(dir, "project.json"), project); err != nil {
			return err
		}
		index = append(index, item)
		logf("projects: %s: %d entities, %d docs, %d commits, %d prs", name, len(own), len(project.Docs),
			project.Activity.CommitsTotal, project.Activity.PRsAuthored)
	}
	sort.SliceStable(index, func(i, j int) bool {
		return index[i].Authored+index[i].PRsAuthored > index[j].Authored+index[j].PRsAuthored
	})
	totals := IndexTotals{Repos: len(index)}
	for _, r := range index {
		totals.Commits += r.CommitsTotal
		totals.AuthoredCommits += r.Authored
		totals.AuthoredOnDefaultBranch += r.AuthoredOnDefaultBranch
		totals.PRsAuthored += r.PRsAuthored
		if r.FirstAuthoredAt != nil && (totals.FirstAuthoredAt == nil || *r.FirstAuthoredAt < *totals.FirstAuthoredAt) {
			totals.FirstAuthoredAt = r.FirstAuthoredAt
		}
		if r.LastAuthoredAt != nil && (totals.LastAuthoredAt == nil || *r.LastAuthoredAt > *totals.LastAuthoredAt) {
			totals.LastAuthoredAt = r.LastAuthoredAt
		}
	}
	err = writeJSON(filepath.Join(h.raw, "index.json"), Index{
		GeneratedAt: nowISO(), Org: h.cfg.Org, GitHubLogin: h.cfg.GitHubLogin, Identity: h.cfg.Identity,
		Schema: "README.md", Totals: totals, Repos: orEmpty(index),
	})
	if err != nil {
		return err
	}
	if len(h.jira.projects) > 0 {
		if err := writeJSON(filepath.Join(h.raw, "org", "jira_projects.json"), h.jira.projects); err != nil {
			return err
		}
	}
	if err := writeFileAtomic(filepath.Join(h.raw, "README.md"), footprint.DataSchema); err != nil {
		return err
	}
	logf("projects: indexed %d repos", len(index))
	return nil
}

// ownEntities are the entities describing repo: systems first, then entities nearer the root.
func ownEntities(entities []Entity, repo string) []*Entity {
	rank := map[string]int{"System": 0, "Component": 1, "API": 2, "Resource": 3}
	kindRank := func(e *Entity) int {
		if r, ok := rank[asString(e.Kind)]; ok {
			return r
		}
		return 9
	}
	var own []*Entity
	for i := range entities {
		if deref(entities[i].SourceRepo) == repo {
			own = append(own, &entities[i])
		}
	}
	sort.SliceStable(own, func(i, j int) bool {
		a, b := own[i], own[j]
		if ka, kb := kindRank(a), kindRank(b); ka != kb {
			return ka < kb
		}
		if da, db := strings.Count(a.Path, "/"), strings.Count(b.Path, "/"); da != db {
			return da < db
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.DocIndex < b.DocIndex
	})
	return own
}

// resolveContext follows what the repo's entities are part of (system -> domain, owner, parent).
func resolveContext(own []*Entity, byRef map[string]*Entity, docs map[*Entity]any) map[string]*ContextEntity {
	related := map[string]*ContextEntity{}
	ownRefs := map[string]bool{}
	var queue []string
	for _, e := range own {
		ownRefs[e.Ref] = true
		queue = append(queue, specRefs(docs[e])...)
	}
	for len(queue) > 0 {
		ref := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if _, done := related[ref]; done || ownRefs[ref] {
			continue
		}
		target := byRef[ref]
		if target == nil {
			related[ref] = nil
			continue
		}
		related[ref] = &ContextEntity{Kind: target.Kind, FoundInRepo: target.FoundInRepo, Path: target.Path, Entity: target.Entity}
		queue = append(queue, specRefs(docs[target])...)
	}
	return related
}

// specRefs are the entity refs that say what an entity is part of. Backstage refs are
// [kind:][namespace/]name, with the kind defaulting by field.
func specRefs(entity any) []string {
	spec := field(entity, "spec")
	var refs []string
	for _, f := range []struct{ field, kind string }{
		{"system", "system"}, {"domain", "domain"}, {"owner", "group"}, {"subcomponentOf", "component"}, {"parent", "group"},
	} {
		value, ok := field(spec, f.field).(string)
		if !ok || value == "" {
			continue
		}
		kind, rest := f.kind, value
		if i := strings.LastIndex(value, ":"); i >= 0 {
			kind, rest = value[:i], value[i+1:]
		}
		namespace, name := "default", rest
		if i := strings.LastIndex(rest, "/"); i >= 0 {
			namespace, name = rest[:i], rest[i+1:]
		}
		refs = append(refs, strings.ToLower(kind)+":"+strings.ToLower(namespace)+"/"+strings.ToLower(name))
	}
	return refs
}

func parentDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// projectDocs saves the repo's docs (root README, CLAUDE.md, AGENTS.md, docs index, package.json,
// and READMEs next to catalog-info files) and lists its layout to depth 2.
func (h *harness) projectDocs(ctx context.Context, repo Repo, catalogDirs map[string]bool) ([]Doc, []string, error) {
	path, ref := h.mirrorPath(repo.Name), "refs/heads/"+repo.defaultBranch()
	docs, top := []Doc{}, []string{}
	if repo.defaultBranch() == "" || !hasRef(ctx, path, ref) {
		return docs, top, nil
	}
	tree, err := lsTree(ctx, path, ref)
	if err != nil {
		return nil, nil, err
	}
	outDir := filepath.Join(h.repoDir(repo.Name), "docs")
	if err := os.RemoveAll(outDir); err != nil {
		return nil, nil, err
	}
	for _, file := range tree {
		dir := parentDir(file)
		base := file[len(dir):]
		base = strings.TrimPrefix(base, "/")
		if !rootDocsRE.MatchString(file) && !(dir != "" && catalogDirs[dir] && readmeRE.MatchString(base)) {
			continue
		}
		blob, err := git(ctx, path, nil, "cat-file", "blob", ref+":"+file)
		if err != nil {
			return nil, nil, err
		}
		keep := blob[:min(len(blob), maxDocBytes)]
		if err := writeUnder(outDir, file, []byte(strings.ToValidUTF8(string(keep), "�"))); err != nil {
			return nil, nil, err
		}
		docs = append(docs, Doc{Path: file, SavedAs: "docs/" + file, Bytes: len(blob), Truncated: len(blob) > maxDocBytes})
	}
	layout := map[string]bool{}
	for _, file := range tree {
		parts := strings.Split(file, "/")
		if len(parts) > 1 {
			layout[parts[0]+"/"] = true
		} else {
			layout[parts[0]] = true
		}
		if len(parts) > 2 {
			layout[parts[0]+"/"+parts[1]+"/"] = true
		}
	}
	top = sortedKeys(layout)
	return docs, top[:min(len(top), treeTopLimit)], nil
}

func (h *harness) historyInfo(ctx context.Context, repo Repo, status MirrorStatus) (*History, error) {
	path := h.mirrorPath(repo.Name)
	out, err := git(ctx, path, nil, "log", "--all", "--max-parents=0", "--format=%aI")
	if err != nil {
		return nil, err
	}
	roots := strings.Fields(string(out))
	var first *string
	for _, r := range roots {
		if first == nil || parseTime(r).Before(parseTime(*first)) {
			first = &r
		}
	}
	count, err := git(ctx, path, nil, "rev-list", "--all", "--count")
	if err != nil {
		return nil, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(count)))
	hist := &History{FirstCommitDate: first, RootCommits: len(roots), CommitsAllRefs: n,
		Branches: status.Branches, Tags: status.Tags, PullRequestRefs: status.PullHeads}
	if status.FetchedAt != "" {
		hist.MirroredAt = &status.FetchedAt
	}
	return hist, nil
}

func summarizeActivity(commits []Commit, prs []PullRequest) Activity {
	a := Activity{CommitsTotal: len(commits), CommitsByRoles: newOrderedMap(), PRsAuthored: len(prs)}
	roleCounts := map[string]int{}
	var roleOrder []string
	jira := map[string]bool{}
	var firstAuthored, lastAuthored string
	for _, c := range commits {
		key := strings.Join(c.Roles, "+")
		if roleCounts[key] == 0 {
			roleOrder = append(roleOrder, key)
		}
		roleCounts[key]++
		for _, k := range c.JiraKeys {
			jira[k] = true
		}
		if !slices.Contains(c.Roles, "author") {
			continue
		}
		a.Authored++
		if c.OnDefaultBranch {
			a.AuthoredOnDefaultBranch++
		} else {
			a.AuthoredOnlyOnOtherRefs++
		}
		if c.IsMerge {
			a.AuthoredMerges++
		} else if c.Stats != nil {
			a.AuthoredAdditions += c.Stats.Additions
			a.AuthoredDeletions += c.Stats.Deletions
		}
		t := parseTime(c.Author.Date)
		if firstAuthored == "" || t.Before(parseTime(firstAuthored)) {
			firstAuthored = c.Author.Date
		}
		if lastAuthored == "" || !t.Before(parseTime(lastAuthored)) {
			lastAuthored = c.Author.Date
		}
	}
	sort.SliceStable(roleOrder, func(i, j int) bool { return roleCounts[roleOrder[i]] > roleCounts[roleOrder[j]] })
	for _, k := range roleOrder {
		a.CommitsByRoles.set(k, roleCounts[k])
	}
	a.FirstAuthoredAt, a.LastAuthoredAt = isoUTC(firstAuthored), isoUTC(lastAuthored)
	var firstPR, lastPR string
	for _, p := range prs {
		for _, k := range p.JiraKeys {
			jira[k] = true
		}
		if deref(p.MergedAt) != "" {
			a.PRsMerged++
		}
		t := parseTime(p.CreatedAt)
		if firstPR == "" || t.Before(parseTime(firstPR)) {
			firstPR = p.CreatedAt
		}
		if lastPR == "" || !t.Before(parseTime(lastPR)) {
			lastPR = p.CreatedAt
		}
	}
	if firstPR != "" {
		a.FirstPRAt, a.LastPRAt = &firstPR, &lastPR
	}
	a.JiraKeys = len(jira)
	return a
}
