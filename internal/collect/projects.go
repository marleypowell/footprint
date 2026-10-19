package collect

import (
	"context"
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
	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/doc"
	"github.com/marleypowell/footprint/internal/tracker"
	"github.com/marleypowell/footprint/internal/wiki"
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
	Name     string         `json:"name"`
	URL      string         `json:"url"`
	CodeHost Repo           `json:"code_host"`
	History  *History       `json:"history"`
	Catalog  ProjectCatalog `json:"catalog"`
	Docs     []Doc          `json:"docs"`
	TreeTop  []string       `json:"tree_top"`
	Activity Activity       `json:"activity"`
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

type ProjectCatalog struct {
	Entities []catalog.Entity `json:"entities"`
	// Context holds the entities those are part of (system, domain, owner, parent), resolved
	// transitively; null when referenced but not defined in any descriptor.
	Context map[string]*catalog.Entity `json:"context"`
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
	CommitsByRoles          *doc.Object `json:"commits_by_roles"` // most common first
	Authored                int         `json:"authored"`
	AuthoredOnDefaultBranch int         `json:"authored_on_default_branch"`
	AuthoredOnlyOnOtherRefs int         `json:"authored_only_on_other_refs"`
	AuthoredMerges          int         `json:"authored_merges"`
	AuthoredAdditions       int         `json:"authored_additions"`
	AuthoredDeletions       int         `json:"authored_deletions"`
	FirstAuthoredAt         *string     `json:"first_authored_at"`
	LastAuthoredAt          *string     `json:"last_authored_at"`
	IssueKeys               int         `json:"issue_keys"`
	PRsAuthored             int         `json:"prs_authored"`
	PRsMerged               int         `json:"prs_merged"`
	FirstPRAt               *string     `json:"first_pr_at"`
	LastPRAt                *string     `json:"last_pr_at"`
}

// Index is <data>/index.json, the entry point to the dataset.
type Index struct {
	GeneratedAt string         `json:"generated_at"`
	Org         string         `json:"org"`
	Login       string         `json:"login"`
	Sources     Sources        `json:"sources"`
	Identity    IdentityConfig `json:"identity"`
	Schema      string         `json:"schema"`
	Totals      IndexTotals    `json:"totals"`
	Repos       []IndexRepo    `json:"repos"`
	// IssuesByProject and WikiBySpace list the issues/ and wiki/ files, most records first.
	IssuesByProject []IndexGroup `json:"issues_by_project"`
	WikiBySpace     []IndexGroup `json:"wiki_by_space"`
}

// Sources names the adapter that produced each part of the dataset; IssueTracker and Wiki are null
// without one.
type Sources struct {
	CodeHost     string  `json:"code_host"`
	Catalog      string  `json:"catalog"`
	IssueTracker *string `json:"issue_tracker"`
	Wiki         *string `json:"wiki"`
}

type IndexTotals struct {
	Repos                   int     `json:"repos"`
	Commits                 int     `json:"commits"`
	AuthoredCommits         int     `json:"authored_commits"`
	AuthoredOnDefaultBranch int     `json:"authored_on_default_branch"`
	PRsAuthored             int     `json:"prs_authored"`
	FirstAuthoredAt         *string `json:"first_authored_at"`
	LastAuthoredAt          *string `json:"last_authored_at"`
	Issues                  int     `json:"issues"`
	WikiPages               int     `json:"wiki_pages"`
}

// IndexGroup is one file of issues (an issue project) or of wiki content (a space).
type IndexGroup struct {
	Key            string      `json:"key"`
	Name           string      `json:"name"`
	File           string      `json:"file"`
	Records        int         `json:"records"`
	ByRole         *doc.Object `json:"by_role"` // records with each role, in the records' role order
	FirstCreatedAt *string     `json:"first_created_at"`
	LastUpdatedAt  *string     `json:"last_updated_at"`
}

type IndexRepo struct {
	Name              string            `json:"name"`
	URL               string            `json:"url"`
	Dir               string            `json:"dir"`
	Files             map[string]string `json:"files"`
	Archived          bool              `json:"archived"`
	Description       *string           `json:"description"`
	Catalog           []IndexEntity     `json:"catalog"`
	Docs              []string          `json:"docs"`
	FirstCommitInRepo *string           `json:"first_commit_in_repo"`
	CreatedAt         string            `json:"created_at"`
	Activity
}

type IndexEntity struct {
	Ref         string  `json:"ref"`
	Category    string  `json:"category"`
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Type        *string `json:"type"`
}

// projects writes each active repo's project.json and docs, then index.json, the issue tracker's
// projects and the schema doc.
func (h *harness) projects(ctx context.Context) error {
	repos, err := h.loadRepos()
	if err != nil {
		return err
	}
	var trackerProjects []tracker.Project // listed first, so that a tracker failing fails fast
	if h.tools.Tracker != nil {
		if trackerProjects, err = h.tools.Tracker.Projects(ctx); err != nil {
			return trackerProjectsError(err)
		}
	}
	byName := map[string]Repo{}
	for _, r := range repos {
		byName[r.Name] = r
	}
	stored, err := readJSONL[catalog.Entity](filepath.Join(h.raw, "catalog", "entities.jsonl"))
	if err != nil {
		return err
	}
	var entities []catalog.Entity
	for _, e := range stored {
		if !e.IsTemplateSkeleton {
			entities = append(entities, e)
		}
	}
	byRef := map[string]*catalog.Entity{}
	for i := range entities {
		if _, seen := byRef[entities[i].Ref]; !seen {
			byRef[entities[i].Ref] = &entities[i]
		}
	}
	statuses := map[string]MirrorStatus{}
	if _, err := readJSON(h.mirrorStatusPath(), &statuses); err != nil {
		return err
	}

	dirEntries, err := os.ReadDir(filepath.Join(h.raw, "repos"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var index []IndexRepo
	for _, entry := range dirEntries {
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
		catalogDirs := map[string]bool{}
		for _, e := range own {
			if e.FoundInRepo == name {
				catalogDirs[parentDir(e.Path)] = true
			}
		}
		project := Project{Name: name, URL: repo.URL, CodeHost: repo, Docs: []Doc{}, TreeTop: []string{},
			Catalog: ProjectCatalog{Entities: []catalog.Entity{}, Context: relatedEntities(own, byRef)}}
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
		prs, err := readJSONL[PullRequestRecord](filepath.Join(dir, "prs.jsonl"))
		if err != nil {
			return err
		}
		project.Activity = summarizeActivity(commits, prs)
		item := IndexRepo{
			Name: name, URL: repo.URL, Dir: "repos/" + name, Files: map[string]string{}, Archived: repo.IsArchived,
			Description: repo.Description, Catalog: []IndexEntity{}, Docs: []string{},
			CreatedAt: repo.CreatedAt, Activity: project.Activity,
		}
		for _, e := range own {
			project.Catalog.Entities = append(project.Catalog.Entities, *e)
			if catalog.DescribesSoftware(e.Category) {
				item.Catalog = append(item.Catalog, IndexEntity{Ref: e.Ref, Category: e.Category, Title: e.Title,
					Description: e.Description, Type: e.Type})
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
	issueProjects, err := indexGroups(filepath.Join(h.raw, "issues"), "issues/", issueRoles, issueGroup)
	if err != nil {
		return err
	}
	wikiSpaces, err := indexGroups(filepath.Join(h.raw, "wiki"), "wiki/", pageRoles, spaceGroup)
	if err != nil {
		return err
	}
	totals.Issues, totals.WikiPages = records(issueProjects), records(wikiSpaces)
	sources := Sources{CodeHost: h.cfg.CodeHost.Kind, Catalog: h.cfg.Catalog.Kind}
	if h.cfg.IssueTracker != nil {
		sources.IssueTracker = &h.cfg.IssueTracker.Kind
	}
	if h.cfg.Wiki != nil {
		sources.Wiki = &h.cfg.Wiki.Kind
	}
	err = writeJSON(filepath.Join(h.raw, "index.json"), Index{
		GeneratedAt: nowISO(), Org: h.cfg.Org, Login: h.cfg.Login, Sources: sources,
		Identity: h.cfg.Identity, Schema: "README.md", Totals: totals, Repos: orEmpty(index),
		IssuesByProject: issueProjects, WikiBySpace: wikiSpaces,
	})
	if err != nil {
		return err
	}
	issueProjectsFile := filepath.Join(h.raw, "org", "issue_projects.json")
	if h.tools.Tracker == nil {
		if err := os.Remove(issueProjectsFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	} else if err := writeJSON(issueProjectsFile, orEmpty(trackerProjects)); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(h.raw, "README.md"), footprint.DataSchema); err != nil {
		return err
	}
	logf("projects: indexed %d repos, %d issue projects and %d wiki spaces", len(index), len(issueProjects), len(wikiSpaces))
	return nil
}

// refreshIndex rewrites index.json after update, when the projects stage has written one: the
// issues and pages stages run after it and refresh their own parts.
func (h *harness) refreshIndex(update func(*Index)) error {
	path := filepath.Join(h.raw, "index.json")
	var index Index
	ok, err := readJSON(path, &index)
	if err != nil || !ok {
		if !ok && err == nil {
			logf("no index.json yet: the projects stage will index this")
		}
		return err
	}
	update(&index)
	index.GeneratedAt = nowISO()
	return writeJSON(path, index)
}

func issueGroup(r indexedRecord) (string, string) { return r.Project.Key, r.Project.Name }
func spaceGroup(r indexedRecord) (string, string) { return r.Space.Key, r.Space.Name }

func records(groups []IndexGroup) int {
	n := 0
	for _, g := range groups {
		n += g.Records
	}
	return n
}

// indexedRecord is the part of an issue or page record the index reads.
type indexedRecord struct {
	Project   tracker.ProjectRef `json:"project"`
	Space     wiki.Space         `json:"space"`
	Roles     []string           `json:"roles"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
}

// indexGroups summarises each .jsonl file in dir (issues/ or wiki/), most records first.
func indexGroups(dir, prefix string, roles []string, group func(indexedRecord) (key, name string)) ([]IndexGroup, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	groups := []IndexGroup{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		rows, err := readJSONL[indexedRecord](filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			continue
		}
		g := IndexGroup{File: prefix + e.Name(), Records: len(rows), ByRole: doc.NewObject()}
		g.Key, g.Name = group(rows[0])
		counts := map[string]int{}
		for _, r := range rows {
			for _, role := range r.Roles {
				counts[role]++
			}
			if r.CreatedAt != "" && (g.FirstCreatedAt == nil || r.CreatedAt < *g.FirstCreatedAt) {
				g.FirstCreatedAt = ptr(r.CreatedAt)
			}
			if r.UpdatedAt != "" && (g.LastUpdatedAt == nil || r.UpdatedAt > *g.LastUpdatedAt) {
				g.LastUpdatedAt = ptr(r.UpdatedAt)
			}
		}
		for _, role := range roles {
			if counts[role] > 0 {
				g.ByRole.Set(role, counts[role])
			}
		}
		groups = append(groups, g)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Records > groups[j].Records })
	return groups, nil
}

// categoryRank orders a repo's entities: systems, then components, APIs and resources, then the rest.
var categoryRank = map[string]int{catalog.System: 0, catalog.Component: 1, catalog.API: 2, catalog.Resource: 3}

// ownEntities are the entities describing repo, systems first, then entities nearer the root.
func ownEntities(entities []catalog.Entity, repo string) []*catalog.Entity {
	rank := func(e *catalog.Entity) int {
		if r, ok := categoryRank[e.Category]; ok {
			return r
		}
		return len(categoryRank)
	}
	var own []*catalog.Entity
	for i := range entities {
		if deref(entities[i].SourceRepo) == repo {
			own = append(own, &entities[i])
		}
	}
	sort.SliceStable(own, func(i, j int) bool {
		a, b := own[i], own[j]
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra < rb
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

// relatedEntities follows what a repo's entities are part of (system -> domain, owner, parent).
func relatedEntities(own []*catalog.Entity, byRef map[string]*catalog.Entity) map[string]*catalog.Entity {
	related := map[string]*catalog.Entity{}
	ownRefs := map[string]bool{}
	var queue []string
	for _, e := range own {
		ownRefs[e.Ref] = true
		queue = append(queue, e.PartOf...)
	}
	for len(queue) > 0 {
		ref := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if _, done := related[ref]; done || ownRefs[ref] {
			continue
		}
		target := byRef[ref]
		related[ref] = target // nil: referenced but not defined anywhere
		if target != nil {
			queue = append(queue, target.PartOf...)
		}
	}
	return related
}

func parentDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// projectDocs saves the repo's docs (root README, CLAUDE.md, AGENTS.md, docs index, package.json,
// and READMEs next to catalog descriptors) and lists its layout to depth 2.
func (h *harness) projectDocs(ctx context.Context, repo Repo, catalogDirs map[string]bool) ([]Doc, []string, error) {
	path, ref := h.mirrorPath(repo.Name), "refs/heads/"+repo.DefaultBranchName()
	docs, top := []Doc{}, []string{}
	if repo.DefaultBranchName() == "" || !hasRef(ctx, path, ref) {
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
		base := strings.TrimPrefix(file[len(dir):], "/")
		if !rootDocsRE.MatchString(file) && !(dir != "" && catalogDirs[dir] && readmeRE.MatchString(base)) {
			continue
		}
		blob, err := git(ctx, path, nil, "cat-file", "blob", ref+":"+file)
		if err != nil {
			return nil, nil, err
		}
		keep := blob[:min(len(blob), maxDocBytes)]
		written, err := writeUnder(outDir, file, []byte(strings.ToValidUTF8(string(keep), "�")))
		if err != nil {
			return nil, nil, err
		}
		if !written {
			continue
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

func summarizeActivity(commits []Commit, prs []PullRequestRecord) Activity {
	a := Activity{CommitsTotal: len(commits), CommitsByRoles: doc.NewObject(), PRsAuthored: len(prs)}
	roleCounts := map[string]int{}
	var roleOrder []string
	issues := map[string]bool{}
	var firstAuthored, lastAuthored string
	for _, c := range commits {
		key := strings.Join(c.Roles, "+")
		if roleCounts[key] == 0 {
			roleOrder = append(roleOrder, key)
		}
		roleCounts[key]++
		for _, k := range c.IssueKeys {
			issues[k] = true
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
		a.CommitsByRoles.Set(k, roleCounts[k])
	}
	a.FirstAuthoredAt, a.LastAuthoredAt = isoUTC(firstAuthored), isoUTC(lastAuthored)
	var firstPR, lastPR string
	for _, p := range prs {
		for _, k := range p.IssueKeys {
			issues[k] = true
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
	a.IssueKeys = len(issues)
	return a
}
