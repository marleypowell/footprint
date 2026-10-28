package facts

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/collect"
)

// Review is one line of reviews.jsonl: another person's PR that the user reviewed or commented on.
// It is the user's reviewing, never their change: its ref says so.
type Review struct {
	Ref         string       `json:"ref"` // review+<repo>#<number>
	Repo        string       `json:"repo"`
	Number      int          `json:"number"`
	URL         string       `json:"url"`
	Title       string       `json:"title"`
	Author      string       `json:"author"`       // the PR's author, on the code host
	AuthorClass string       `json:"author_class"` // colleague, agent or bot
	State       string       `json:"state"`        // the PR's: merged, closed or open
	Keys        []string     `json:"keys"`         // the issues in the dataset it names, by current key
	Item        string       `json:"item"`         // the item it counts toward: one of the user's that it names; "" for none
	First       string       `json:"first"`        // the first and last day of the user's reviews and comments on it
	Last        string       `json:"last"`
	Reviews     ReviewStates `json:"reviews"`  // the user's submitted reviews, by state
	Comments    int          `json:"comments"` // the user's comments: on lines of the diff, and on the conversation
	Words       int          `json:"words"`    // in the user's review bodies and comments

	texts []packText // the user's words, for the pack of the item it counts toward
	evs   []event
}

// ReviewStates count reviews by their state.
type ReviewStates struct {
	Approved         int `json:"approved"`
	ChangesRequested int `json:"changes_requested"`
	Commented        int `json:"commented"`
	Dismissed        int `json:"dismissed"`
}

// did says what the user did on the PR: "approved, changes requested; 2 comments".
func (r *Review) did() string {
	var parts []string
	for _, s := range []struct {
		n    int
		what string
	}{{r.Reviews.ChangesRequested, "changes requested"}, {r.Reviews.Approved, "approved"}, {r.Reviews.Commented, "commented"},
		{r.Reviews.Dismissed, "a review dismissed"}} {
		if s.n > 0 {
			parts = append(parts, s.what)
		}
	}
	out := strings.Join(parts, ", ")
	if r.Comments > 0 {
		if out != "" {
			out += "; "
		}
		out += itoa(r.Comments) + " comment"
		if r.Comments > 1 {
			out += "s"
		}
	}
	return out
}

func reviewRef(repo string, number int) string { return "review+" + repo + "#" + itoa(number) }

// buildReviews makes a record of each reviewed PR, and counts it toward the item of the issue it
// names, when that item is one of the user's: reviews never make items.
func (f *Facts) buildReviews() {
	f.reviews = map[string]*Review{}
	for i := range f.raw.Reviews {
		rec := &f.raw.Reviews[i]
		r := &Review{Ref: reviewRef(rec.Repo, rec.Number), Repo: rec.Repo, Number: rec.Number, URL: rec.URL, Title: rec.Title,
			Author: deref(rec.Author), State: strings.ToLower(rec.State), Keys: f.knownKeys(rec.IssueKeys)}
		switch class := f.people.class(r.Author, ""); {
		case rec.AuthorIsBot || class == Bot:
			r.AuthorClass = Bot
		case class == Agent:
			r.AuthorClass = Agent
		default:
			r.AuthorClass = Colleague
		}
		if len(r.Keys) > 0 {
			if id, _ := f.itemOf(f.bestKey(r.Keys)); f.items[id] != nil {
				r.Item = id
			}
		}
		var days []string
		mark := func(at time.Time, kind string) {
			if at.IsZero() {
				return
			}
			r.evs = append(r.evs, event{at: at, kind: kind, key: r.Ref, item: r.Item, repo: r.Repo, who: r.Author},
				event{at: at, kind: evActive, key: r.Ref, repo: r.Repo})
			days = append(days, day(at))
		}
		say := func(at time.Time, text string) {
			if text = strings.TrimSpace(text); text != "" {
				r.Words += len(strings.Fields(text))
				r.texts = append(r.texts, packText{Ref: r.Ref, Day: day(at), Text: clip(oneLine(text), maxComment)})
			}
		}
		for _, rv := range rec.Reviews {
			at, _ := parseTime(rv.SubmittedAt)
			kind := evReview
			switch rv.State {
			case codehost.ReviewApproved:
				r.Reviews.Approved++
			case codehost.ReviewChangesRequested:
				r.Reviews.ChangesRequested++
				kind = evChangesRequested
			case codehost.ReviewCommented:
				r.Reviews.Commented++
			case codehost.ReviewDismissed:
				r.Reviews.Dismissed++
			}
			if r.AuthorClass == Bot {
				kind = evBotReview
			}
			mark(at, kind)
			say(at, rv.Body)
			for _, c := range rv.Comments {
				cat, _ := parseTime(c.CreatedAt)
				r.Comments++
				mark(cat, evReviewComment)
				say(cat, c.Body)
			}
		}
		for _, c := range rec.Comments {
			at, _ := parseTime(c.CreatedAt)
			r.Comments++
			mark(at, evReviewComment)
			say(at, c.Body)
		}
		if len(days) == 0 {
			continue
		}
		slices.Sort(days)
		r.First, r.Last = days[0], days[len(days)-1]
		f.Reviews = append(f.Reviews, r)
		f.reviews[r.Ref] = r
		if it := f.items[r.Item]; it != nil {
			it.Reviews = append(it.Reviews, r.Ref)
		}
	}
	slices.SortFunc(f.Reviews, func(a, b *Review) int {
		return cmp.Or(cmp.Compare(a.Repo, b.Repo), cmp.Compare(a.Number, b.Number))
	})
	for _, it := range f.items {
		slices.SortFunc(it.Reviews, collect.NaturalCompare)
	}
}
