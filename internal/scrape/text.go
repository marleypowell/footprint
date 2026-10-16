package scrape

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// jiraProject is one entry of the Jira projects file (exported with the Atlassian connector).
type jiraProject struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	ProjectTypeKey string `json:"projectTypeKey"`
	Archived       bool   `json:"archived"`
}

// jiraMatcher finds Jira keys for known projects. Without known projects it finds nothing: a bare
// KEY-123 pattern matches too much (UTF-8, ISO-8601, dotnet-8...).
type jiraMatcher struct {
	projects []jiraProject
	keys     map[string]bool
}

func loadJira(cfg *Config) (*jiraMatcher, error) {
	j := &jiraMatcher{keys: map[string]bool{}}
	if f := cfg.Jira.ProjectsFile; f != nil && *f != "" {
		if _, err := readJSON(cfg.resolve(*f), &j.projects); err != nil {
			return nil, err
		}
	}
	for _, p := range j.projects {
		j.keys[strings.ToUpper(p.Key)] = true
	}
	for _, k := range cfg.Jira.ExtraProjectKeys {
		j.keys[strings.ToUpper(k)] = true
	}
	return j, nil
}

var (
	// Jira issue numbers never have leading zeros, which also rules out analyzer IDs like ACME0901.
	jiraRE       = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]{1,9}-[1-9][0-9]{0,5}`)
	jiraNoDashRE = regexp.MustCompile(`[A-Za-z]{4,10}[1-9][0-9]{2,5}`) // "SHOP3481" typos
	guidRE       = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

// find returns the sorted, de-duplicated Jira keys (case-insensitive) in the given texts.
func (j *jiraMatcher) find(texts ...string) []string {
	found := map[string]bool{}
	add := func(project, number string) {
		if project = strings.ToUpper(project); j.keys[project] {
			found[project+"-"+number] = true
		}
	}
	for _, text := range texts {
		text = guidRE.ReplaceAllString(text, " ")
		for _, m := range findBounded(jiraRE, text) {
			i := strings.IndexByte(m, '-')
			add(m[:i], m[i+1:])
		}
		for _, m := range findBounded(jiraNoDashRE, text) {
			i := strings.IndexAny(m, "123456789")
			add(m[:i], m[i:])
		}
	}
	return sortedKeys(found)
}

// findBounded returns the matches of re that are not preceded by an ASCII letter or digit and not
// followed by a digit: the lookarounds RE2 lacks. A rejected match resumes the search one byte
// later, as a backtracking engine would.
func findBounded(re *regexp.Regexp, text string) []string {
	var out []string
	for pos := 0; pos < len(text); {
		loc := re.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		start, end := pos+loc[0], pos+loc[1]
		if (start > 0 && isASCIIAlnum(text[start-1])) || (end < len(text) && isDigit(text[end])) {
			pos = start + 1
			continue
		}
		out = append(out, text[start:end])
		pos = end
	}
	return out
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isASCIIAlnum(c byte) bool { return isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'z') }

var (
	githubPRInMessage = []*regexp.Regexp{
		regexp.MustCompile(`(?m)\(#(\d+)\)\s*$`),               // squash merge: "Title (#123)"
		regexp.MustCompile(`(?m)^Merge pull request #(\d+)\b`), // merge commit
	}
	adoPRInMessage = regexp.MustCompile(`(?m)^Merged PR (\d+):`) // Azure DevOps-era (pre-GitHub) merges
)

func prNumbersInMessage(message string) []int { return numbersMatching(message, githubPRInMessage...) }

func adoPRNumbersInMessage(message string) []int { return numbersMatching(message, adoPRInMessage) }

func numbersMatching(text string, res ...*regexp.Regexp) []int {
	seen := map[int]bool{}
	for _, re := range res {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil {
				seen[n] = true
			}
		}
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}
