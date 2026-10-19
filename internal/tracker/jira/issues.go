package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/marleypowell/footprint/internal/atlassian"
	"github.com/marleypowell/footprint/internal/atlassian/adf"
	"github.com/marleypowell/footprint/internal/logx"
	"github.com/marleypowell/footprint/internal/par"
	"github.com/marleypowell/footprint/internal/tracker"
)

const (
	parallelism = 8
	// maxChangeValue caps a change's from/to text. Text fields (descriptions, checklist plugins)
	// repeat their whole old and new value in every change, which would dominate the dataset; the
	// current value is kept in full in the record.
	maxChangeValue = 300
)

// Custom field types the issue record reads into its own fields.
const (
	sprintType      = "com.pyxis.greenhopper.jira:gh-sprint"
	storyPointsType = "com.pyxis.greenhopper.jira:jsw-story-points"
	teamType        = "com.atlassian.teams:rm-teams-custom-field-team"
	oldTeamType     = "com.atlassian.jira.plugin.system.customfieldtypes:atlassian-team"
	rankType        = "com.pyxis.greenhopper.jira:gh-lexo-rank"
)

const (
	// embeddedChanges is how many changes GET issue embeds: the newest ones. Its total may count
	// only those, so a full page means paging the rest.
	embeddedChanges = 100
	// maxFailures is how many issues may fail to load (after retries) before the stage gives up.
	maxFailures = 20
)

var (
	// recordFields are the system fields an Issue holds in its own shape.
	recordFields = set("summary", "description", "issuetype", "project", "status", "statusCategory", "resolution",
		"priority", "labels", "components", "fixVersions", "parent", "subtasks", "issuelinks", "assignee", "reporter",
		"creator", "created", "updated", "resolutiondate", "duedate", "comment", "worklog", "attachment")
	// viewerFields describe the token holder's own view of the issue (watching, voted, viewed), or
	// are derived noise.
	viewerFields = set("lastViewed", "watches", "votes", "issuerestriction", "workratio")
	// noiseKeys only serve Jira's UI and API links; emailAddress is colleagues' addresses.
	noiseKeys = set("self", "avatarUrls", "avatarUrl", "iconUrl", "avatarId", "expand", "emailAddress")

	statusCategories = map[string]string{"new": "todo", "indeterminate": "in_progress", "done": "done"}
)

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

type fieldDef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Schema struct {
		Custom string `json:"custom"`
	} `json:"schema"`
}

// fieldIDs are the custom fields that hold the record's sprints, estimate and team.
type fieldIDs struct {
	names                     map[string]string // field ID -> name
	sprints, estimates, teams []string
	skip                      map[string]bool // also excluded from Issue.Fields
}

func loadFields(ctx context.Context, api *atlassian.Client) (fieldIDs, error) {
	var defs []fieldDef
	if err := api.Get(ctx, "/rest/api/3/field", nil, &defs); err != nil {
		return fieldIDs{}, err
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].ID < defs[j].ID })
	f := fieldIDs{names: map[string]string{}, skip: map[string]bool{}}
	for _, d := range defs {
		f.names[d.ID] = d.Name
		switch {
		case d.Schema.Custom == sprintType:
			f.sprints = append(f.sprints, d.ID)
		case d.Schema.Custom == storyPointsType || strings.EqualFold(d.Name, "Story Points") ||
			strings.EqualFold(d.Name, "Story point estimate"):
			f.estimates = append(f.estimates, d.ID)
		case d.Schema.Custom == teamType || d.Schema.Custom == oldTeamType:
			f.teams = append(f.teams, d.ID)
		case d.Schema.Custom != rankType:
			continue
		}
		f.skip[d.ID] = true
	}
	return f, nil
}

// Issues loads each issue with one GET (every field, plus the newest changes), then pages through
// the comments, worklogs or history of the issues that have more than that response holds.
func (t *Tracker) Issues(ctx context.Context, keys []string) ([]tracker.Issue, []tracker.Missing, error) {
	api, err := t.client(ctx)
	if err != nil {
		return nil, nil, err
	}
	fields, err := loadFields(ctx, api)
	if err != nil {
		return nil, nil, err
	}
	loaded := make([]*tracker.Issue, len(keys))
	misses := make([]*tracker.Missing, len(keys))
	var done, failures atomic.Int64
	err = par.ForEach(ctx, parallelism, keys, func(ctx context.Context, i int, key string) error {
		issue, err := loadIssue(ctx, api, fields, key)
		switch code := atlassian.StatusCode(err); {
		case err == nil:
			loaded[i] = issue
		case ctx.Err() != nil:
			return ctx.Err()
		case code == http.StatusUnauthorized: // the token stopped working
			return fmt.Errorf("%s: %w", key, err)
		case code == http.StatusNotFound || code == http.StatusBadRequest:
			misses[i] = &tracker.Missing{Key: key, Reason: tracker.NotFound}
		case code == http.StatusForbidden:
			misses[i] = &tracker.Missing{Key: key, Reason: tracker.Hidden}
		default:
			logx.Printf("jira: %s: %v", key, err)
			misses[i] = &tracker.Missing{Key: key, Reason: tracker.Failed, Error: err.Error()}
			if failures.Add(1) > maxFailures {
				return fmt.Errorf("more than %d issues failed to load, the last %s: %w", maxFailures, key, err)
			}
		}
		if n := done.Add(1); n%250 == 0 || int(n) == len(keys) {
			logx.Printf("jira: loaded %d/%d issues", n, len(keys))
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	issues, missing := []tracker.Issue{}, []tracker.Missing{}
	byID := map[string]int{}
	for i, issue := range loaded {
		if issue == nil {
			missing = append(missing, *misses[i])
			continue
		}
		if j, seen := byID[issue.ID]; seen { // an old key and the current one, say
			issues[j].PreviousKeys = union(issues[j].PreviousKeys, issue.PreviousKeys)
			continue
		}
		byID[issue.ID] = len(issues)
		issues = append(issues, *issue)
	}
	return issues, missing, nil
}

type rawIssue struct {
	ID        string                     `json:"id"`
	Key       string                     `json:"key"`
	Fields    map[string]json.RawMessage `json:"fields"`
	Changelog struct {
		Total     int          `json:"total"`
		Histories []rawHistory `json:"histories"`
	} `json:"changelog"`
}

type rawUser struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

type rawHistory struct {
	ID      string    `json:"id"`
	Author  *rawUser  `json:"author"`
	Created string    `json:"created"`
	Items   []rawItem `json:"items"`
}

type rawItem struct {
	Field      string  `json:"field"`
	FieldID    string  `json:"fieldId"`
	From       *string `json:"from"`
	FromString *string `json:"fromString"`
	To         *string `json:"to"`
	ToString   *string `json:"toString"`
}

type rawComment struct {
	ID      string          `json:"id"`
	Author  *rawUser        `json:"author"`
	Body    json.RawMessage `json:"body"`
	Created string          `json:"created"`
	Updated string          `json:"updated"`
}

type rawWorklog struct {
	ID               string          `json:"id"`
	Author           *rawUser        `json:"author"`
	Comment          json.RawMessage `json:"comment"`
	Started          string          `json:"started"`
	TimeSpentSeconds int             `json:"timeSpentSeconds"`
}

// loadIssue loads one issue. Its error carries the API's status: 404 for an issue that does not
// exist or that the account may not see (GET issue finds a moved issue by its old key).
func loadIssue(ctx context.Context, api *atlassian.Client, fields fieldIDs, key string) (*tracker.Issue, error) {
	var raw rawIssue
	err := api.Get(ctx, "/rest/api/3/issue/"+url.PathEscape(key), url.Values{"fields": {"*all"}, "expand": {"changelog"}}, &raw)
	if err != nil {
		return nil, err
	}
	base := "/rest/api/3/issue/" + url.PathEscape(raw.ID)
	histories := raw.Changelog.Histories
	if raw.Changelog.Total > len(histories) || len(histories) >= embeddedChanges {
		if histories, err = morePages(ctx, api, histories, base+"/changelog", "values", 100, nil); err != nil {
			return nil, err
		}
	}
	comments := field[struct {
		Comments []rawComment `json:"comments"`
		Total    int          `json:"total"`
	}](raw.Fields, "comment")
	if comments.Total > len(comments.Comments) {
		if comments.Comments, err = morePages(ctx, api, comments.Comments, base+"/comment", "comments", 100, url.Values{"orderBy": {"created"}}); err != nil {
			return nil, err
		}
	}
	worklogs := field[struct {
		Worklogs []rawWorklog `json:"worklogs"`
		Total    int          `json:"total"`
	}](raw.Fields, "worklog")
	if worklogs.Total > len(worklogs.Worklogs) {
		if worklogs.Worklogs, err = morePages(ctx, api, worklogs.Worklogs, base+"/worklog", "worklogs", 1000, nil); err != nil {
			return nil, err
		}
	}
	issue := convert(api.Site(), raw, fields, histories, comments.Comments, worklogs.Worklogs)
	if !strings.EqualFold(key, issue.Key) {
		issue.PreviousKeys = union(issue.PreviousKeys, []string{strings.ToUpper(key)})
	}
	return &issue, nil
}

// morePages replaces the items an issue response embedded with the whole list, from its own
// endpoint. The endpoint answering 404 (worklogs with time tracking off, say) keeps what the
// issue embedded.
func morePages[T any](ctx context.Context, api *atlassian.Client, embedded []T, path, itemsKey string, size int, extra url.Values) ([]T, error) {
	all, err := allPages[T](ctx, api, path, itemsKey, size, extra)
	if atlassian.StatusCode(err) == http.StatusNotFound {
		logx.Printf("jira: %s: not found; keeping the %d items the issue holds", path, len(embedded))
		return embedded, nil
	}
	return all, err
}

// allPages reads every item of a list paginated with startAt and maxResults, kept under itemsKey.
func allPages[T any](ctx context.Context, api *atlassian.Client, path, itemsKey string, size int, extra url.Values) ([]T, error) {
	var all []T
	for {
		params := url.Values{"startAt": {strconv.Itoa(len(all))}, "maxResults": {strconv.Itoa(size)}}
		for k, v := range extra {
			params[k] = v
		}
		var page map[string]json.RawMessage
		if err := api.Get(ctx, path, params, &page); err != nil {
			return nil, err
		}
		var items []T
		if err := json.Unmarshal(orNull(page[itemsKey]), &items); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var total int
		var isLast bool
		_ = json.Unmarshal(orNull(page["total"]), &total)
		_ = json.Unmarshal(orNull(page["isLast"]), &isLast)
		all = append(all, items...)
		if len(items) == 0 || isLast || len(all) >= total {
			return all, nil
		}
	}
}

func orNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

// field decodes one field of an issue; a missing or unexpected value decodes as the zero value.
func field[T any](fields map[string]json.RawMessage, id string) T {
	var v T
	if raw, ok := fields[id]; ok {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}

type named struct {
	Name string `json:"name"`
}

// issueType is an issue's type. hierarchyLevel is the type's level in the site's issue hierarchy
// (-1 sub-task, 0 standard, 1 epic, and above).
type issueType struct {
	Name           string `json:"name"`
	Subtask        bool   `json:"subtask"`
	HierarchyLevel *int   `json:"hierarchyLevel"`
}

// level is the type's tracker level: the site's hierarchyLevel, or failing that the sub-task flag.
func (t issueType) level() int {
	switch {
	case t.HierarchyLevel != nil:
		return *t.HierarchyLevel
	case t.Subtask:
		return tracker.SubtaskLevel
	}
	return tracker.StandardLevel
}

type rawRef struct {
	Key    string `json:"key"`
	Fields struct {
		Summary   string    `json:"summary"`
		Status    named     `json:"status"`
		IssueType issueType `json:"issuetype"`
	} `json:"fields"`
}

func (r *rawRef) ref() tracker.IssueRef {
	t := r.Fields.IssueType
	return tracker.IssueRef{Key: r.Key, Summary: r.Fields.Summary, Type: t.Name, Level: t.level(), Status: r.Fields.Status.Name}
}

func convert(site string, raw rawIssue, ids fieldIDs, histories []rawHistory, comments []rawComment, worklogs []rawWorklog) tracker.Issue {
	f := raw.Fields
	status := field[struct {
		Name           string `json:"name"`
		StatusCategory struct {
			Key string `json:"key"`
		} `json:"statusCategory"`
	}](f, "status")
	project := field[struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}](f, "project")
	kind := field[issueType](f, "issuetype")
	category := statusCategories[status.StatusCategory.Key]
	if category == "" {
		category = status.StatusCategory.Key
	}
	issue := tracker.Issue{
		Key: raw.Key, ID: raw.ID, URL: site + "/browse/" + raw.Key, PreviousKeys: []string{},
		Project: tracker.ProjectRef{Key: project.Key, Name: project.Name},
		Type:    kind.Name, Level: kind.level(), Summary: field[string](f, "summary"),
		Description: richText(f["description"]), Status: status.Name, StatusCategory: category,
		Resolution: namePtr(field[*named](f, "resolution")), Priority: namePtr(field[*named](f, "priority")),
		Labels: nonNil(field[[]string](f, "labels")), Components: names(field[[]named](f, "components")),
		FixVersions: names(field[[]named](f, "fixVersions")), Sprints: []tracker.Sprint{},
		Subtasks: []tracker.IssueRef{}, Links: []tracker.IssueLink{},
		Assignee: user(field[*rawUser](f, "assignee")), Reporter: user(field[*rawUser](f, "reporter")),
		Creator:   user(field[*rawUser](f, "creator")),
		CreatedAt: atlassian.Time(field[string](f, "created")), UpdatedAt: atlassian.Time(field[string](f, "updated")),
		ResolvedAt: atlassian.TimePtr(field[*string](f, "resolutiondate")), DueDate: field[*string](f, "duedate"),
		Comments: []tracker.Comment{}, History: []tracker.Change{}, Worklogs: []tracker.Worklog{},
		Attachments: []tracker.Attachment{}, Fields: otherFields(f, ids),
	}
	if parent := field[*rawRef](f, "parent"); parent != nil {
		ref := parent.ref()
		issue.Parent = &ref
	}
	for _, s := range field[[]rawRef](f, "subtasks") {
		issue.Subtasks = append(issue.Subtasks, s.ref())
	}
	for _, l := range field[[]struct {
		Type struct {
			Inward  string `json:"inward"`
			Outward string `json:"outward"`
		} `json:"type"`
		InwardIssue  *rawRef `json:"inwardIssue"`
		OutwardIssue *rawRef `json:"outwardIssue"`
	}](f, "issuelinks") {
		if l.OutwardIssue != nil {
			issue.Links = append(issue.Links, tracker.IssueLink{Relation: l.Type.Outward, IssueRef: l.OutwardIssue.ref()})
		}
		if l.InwardIssue != nil {
			issue.Links = append(issue.Links, tracker.IssueLink{Relation: l.Type.Inward, IssueRef: l.InwardIssue.ref()})
		}
	}
	for _, id := range ids.sprints {
		for _, s := range field[[]struct {
			Name         string  `json:"name"`
			State        string  `json:"state"`
			Goal         string  `json:"goal"`
			StartDate    *string `json:"startDate"`
			EndDate      *string `json:"endDate"`
			CompleteDate *string `json:"completeDate"`
		}](f, id) {
			issue.Sprints = append(issue.Sprints, tracker.Sprint{Name: s.Name, State: s.State, Goal: s.Goal,
				StartAt: atlassian.TimePtr(s.StartDate), EndAt: atlassian.TimePtr(s.EndDate), CompletedAt: atlassian.TimePtr(s.CompleteDate)})
		}
	}
	sort.SliceStable(issue.Sprints, func(i, j int) bool {
		a, b := issue.Sprints[i].StartAt, issue.Sprints[j].StartAt
		return a != nil && (b == nil || *a < *b) // unstarted sprints last
	})
	for _, id := range ids.estimates {
		if v := field[*float64](f, id); v != nil {
			issue.Estimate = v
			break
		}
	}
	for _, id := range ids.teams {
		team := field[*struct {
			Name  string `json:"name"`
			Title string `json:"title"`
		}](f, id)
		if team != nil && (team.Title != "" || team.Name != "") {
			name := team.Title
			if name == "" {
				name = team.Name
			}
			issue.Team = &name
			break
		}
	}
	for _, a := range field[[]struct {
		ID       string   `json:"id"`
		Filename string   `json:"filename"`
		Size     int64    `json:"size"`
		MimeType string   `json:"mimeType"`
		Author   *rawUser `json:"author"`
		Created  string   `json:"created"`
	}](f, "attachment") {
		issue.Attachments = append(issue.Attachments, tracker.Attachment{ID: a.ID, Filename: a.Filename, Size: a.Size,
			MimeType: a.MimeType, Author: user(a.Author), CreatedAt: atlassian.Time(a.Created)})
	}
	for _, c := range comments {
		issue.Comments = append(issue.Comments, tracker.Comment{ID: c.ID, Author: user(c.Author),
			CreatedAt: atlassian.Time(c.Created), UpdatedAt: atlassian.Time(c.Updated), Body: richText(c.Body)})
	}
	sort.SliceStable(issue.Comments, func(i, j int) bool { return issue.Comments[i].CreatedAt < issue.Comments[j].CreatedAt })
	for _, w := range worklogs {
		issue.Worklogs = append(issue.Worklogs, tracker.Worklog{ID: w.ID, Author: user(w.Author),
			StartedAt: atlassian.Time(w.Started), Seconds: w.TimeSpentSeconds, Comment: richText(w.Comment)})
	}
	sort.SliceStable(issue.Worklogs, func(i, j int) bool { return issue.Worklogs[i].StartedAt < issue.Worklogs[j].StartedAt })
	for _, h := range histories {
		change := tracker.Change{ID: h.ID, Author: user(h.Author), CreatedAt: atlassian.Time(h.Created), Items: []tracker.ChangeItem{}}
		for _, it := range h.Items {
			item := tracker.ChangeItem{Field: it.Field, FieldID: it.FieldID, From: clip(it.FromString), To: clip(it.ToString),
				FromID: changeID(it.From, it.FromString), ToID: changeID(it.To, it.ToString)}
			if it.FieldID == "assignee" || it.FieldID == "status" {
				item.Field = it.FieldID
			}
			if strings.EqualFold(it.Field, "key") && it.FromString != nil && *it.FromString != "" { // moved
				issue.PreviousKeys = union(issue.PreviousKeys, []string{*it.FromString})
			}
			change.Items = append(change.Items, item)
		}
		issue.History = append(issue.History, change)
	}
	sort.SliceStable(issue.History, func(i, j int) bool {
		a, b := issue.History[i], issue.History[j]
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return a.ID < b.ID
	})
	return issue
}

// otherFields keeps every field the record does not already hold, keyed by name, without empty
// values or UI noise. A name two fields share gets the field ID appended.
func otherFields(f map[string]json.RawMessage, ids fieldIDs) map[string]any {
	keys := make([]string, 0, len(f))
	for id := range f {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	out := map[string]any{}
	for _, id := range keys {
		if recordFields[id] || viewerFields[id] || ids.skip[id] {
			continue
		}
		var v any
		if err := json.Unmarshal(f[id], &v); err != nil {
			continue
		}
		if v = clean(v); isBlank(v) {
			continue
		}
		name := ids.names[id]
		if name == "" {
			name = id
		}
		if _, taken := out[name]; taken {
			name += " (" + id + ")"
		}
		out[name] = v
	}
	return out
}

// clean drops UI noise and empty values, and renders rich text as Markdown.
func clean(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if adf.IsDoc(x) {
			if b, err := json.Marshal(x); err == nil {
				if md, err := adf.Markdown(b); err == nil {
					return md
				}
			}
			return x
		}
		out := make(map[string]any, len(x))
		for k, val := range x {
			if c := clean(val); !noiseKeys[k] && !isEmpty(c) {
				out[k] = c
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			if c := clean(e); !isEmpty(c) {
				out = append(out, c)
			}
		}
		return out
	}
	return v
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// isBlank is isEmpty for a whole field, which is also blank when it is an object of zeroes such as
// {"progress": 0, "total": 0}.
func isBlank(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return isEmpty(v)
	}
	for _, val := range m {
		switch x := val.(type) {
		case float64:
			if x != 0 {
				return false
			}
		case bool:
			if x {
				return false
			}
		default:
			if !isEmpty(x) {
				return false
			}
		}
	}
	return true
}

// richText renders a rich text value (an ADF document; plain text from older data) as Markdown.
func richText(raw json.RawMessage) string {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		if md, err := adf.Markdown(raw); err == nil {
			return md
		}
	}
	return ""
}

// changeID keeps a change's raw value (an account, option or status ID) when it says something the
// display value does not; text fields repeat their text in both.
func changeID(raw, display *string) *string {
	if raw == nil || *raw == "" || (display != nil && *raw == *display) {
		return nil
	}
	return raw
}

func clip(s *string) *string {
	if s == nil {
		return nil
	}
	r := []rune(*s)
	if len(r) <= maxChangeValue {
		return s
	}
	cut := fmt.Sprintf("%s… [+%d characters]", string(r[:maxChangeValue]), len(r)-maxChangeValue)
	return &cut
}

func user(u *rawUser) *tracker.User {
	if u == nil || (u.AccountID == "" && u.DisplayName == "") {
		return nil
	}
	return &tracker.User{ID: u.AccountID, Name: u.DisplayName}
}

func namePtr(n *named) *string {
	if n == nil || n.Name == "" {
		return nil
	}
	return &n.Name
}

func names(items []named) []string {
	out := make([]string, 0, len(items))
	for _, n := range items {
		out = append(out, n.Name)
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// union merges two key lists into a sorted, de-duplicated one.
func union(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, k := range append(append([]string{}, a...), b...) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
