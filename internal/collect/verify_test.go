package collect

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

// verify lists the commits the code host attributes to the account that the dataset lacks: each
// is an identity variant, or a ref, the extraction missed.
func TestVerify(t *testing.T) {
	host := &fakeHost{attributed: []codehost.AttributedCommit{
		{Repo: "api", SHA: "c1", Date: "2024-01-05T00:00:00Z"},
		{Repo: "api", SHA: "c1", Date: "2024-01-05T00:00:00Z"}, // listed twice by the host: counted once
		{Repo: "api", SHA: "aaa", Date: "2024-02-01T00:00:00Z", AuthorName: "Jane Doe", AuthorEmail: "jane@laptop.local", Subject: "fix"},
		{Repo: "web", SHA: "bbb", Date: "2024-01-01T00:00:00Z"},
		{Repo: "api", SHA: "ccc", Date: "2024-01-01T00:00:00Z"},
		{Repo: "web", SHA: "c2", Date: "2024-01-06T00:00:00Z"}, // the dataset has c2 in api, not web
	}}
	h := newTestHarness(t, Tools{Host: host})
	writeRows(t, h, "repos/api/commits.jsonl",
		Commit{Repo: "api", SHA: "c1", Roles: []string{"author", "committer"}, OnDefaultBranch: true},
		Commit{Repo: "api", SHA: "c2", Roles: []string{"author"}},                           // only on a branch
		Commit{Repo: "api", SHA: "c3", Roles: []string{"committer"}, OnDefaultBranch: true}) // not authored
	writeRows(t, h, "repos/web/commits.jsonl", Commit{Repo: "web", SHA: "c4", Roles: []string{"author"}, OnDefaultBranch: true})
	if err := h.verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	var v Verification
	if ok, err := readJSON(filepath.Join(h.raw, "org", "verification.json"), &v); !ok || err != nil {
		t.Fatalf("verification.json: %v %v", ok, err)
	}
	counts := []int{v.HostAttributedCommits, v.DatasetCommits, v.DatasetAuthoredOnDefaultBranch, v.AttributedMissingFromDataset}
	if want := []int{5, 4, 2, 4}; !reflect.DeepEqual(counts, want) {
		t.Errorf("attributed, dataset, authored on default, missing = %v, want %v", counts, want)
	}
	var missing []string
	for _, m := range v.Missing {
		missing = append(missing, m.Repo+"/"+m.SHA)
	}
	if want := []string{"api/ccc", "web/bbb", "web/c2", "api/aaa"}; !reflect.DeepEqual(missing, want) {
		t.Errorf("missing = %q, want %q (oldest first, then by repo and sha)", missing, want)
	}
	if got, want := v.Missing[3], (MissingCommit{Repo: "api", SHA: "aaa", Date: "2024-02-01T00:00:00Z",
		Author: "Jane Doe <jane@laptop.local>", Subject: "fix"}); got != want {
		t.Errorf("missing commit = %+v, want %+v", got, want)
	}
}

func TestVerifyBeforeAnyCommits(t *testing.T) {
	h := newTestHarness(t, Tools{Host: &fakeHost{}})
	if err := h.verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	var v Verification
	if ok, err := readJSON(filepath.Join(h.raw, "org", "verification.json"), &v); !ok || err != nil || v.Missing == nil || v.DatasetCommits != 0 {
		t.Errorf("verification.json = %+v, %v %v; want no commits and an empty missing list", v, ok, err)
	}
}
