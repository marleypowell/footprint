package scrape

import (
	"reflect"
	"testing"
)

func TestJiraKeys(t *testing.T) {
	j := &jiraMatcher{keys: map[string]bool{}}
	for _, k := range []string{"SHOP", "APP", "TK", "UI", "CORE", "ACME", "CAFE", "GRO"} {
		j.keys[k] = true
	}
	tests := []struct {
		name, text string
		want       []string
	}{
		{"conventional commit scope", "feat(SHOP-4435): move the shell", []string{"SHOP-4435"}},
		{"lowercase branch name", "jdoe/tk-473", []string{"TK-473"}},
		{"suffix of a feature-flag name", "flag `storefront-enable-order-search-ui-17`", []string{"UI-17"}},
		{"inside a word", "upgrade aspnetcore-8 packages", []string{}},
		{"unknown project", "UTF-8 and ISO-8601", []string{}},
		{"missing dash typo", "SHOP3481 tidy up", []string{"SHOP-3481"}},
		{"leading zero is an analyzer id", "suppress ACME0901 and ACME-0001", []string{}},
		{"seven digits", "SHOP-1234567", []string{}},
		{"adjacent keys", "SHOP-1,SHOP-2 SHOP-3", []string{"SHOP-1", "SHOP-2", "SHOP-3"}},
		{"guid fragments are not keys", "id deadbeef-cafe-1234-abcd-0123456789ab", []string{}},
		{"duplicates collapse", "GRO-12 then GRO-12 again", []string{"GRO-12"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := j.find(tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("find(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
	if got := j.find("APP-2", "app-1"); !reflect.DeepEqual(got, []string{"APP-1", "APP-2"}) {
		t.Errorf("several texts: got %v", got)
	}
	if got := (&jiraMatcher{keys: map[string]bool{}}).find("SHOP-1"); len(got) != 0 {
		t.Errorf("no configured projects should find nothing, got %v", got)
	}
}

func TestPRNumbersInMessage(t *testing.T) {
	tests := []struct {
		message     string
		github, ado []int
	}{
		{"feat: add timeout (#50)", []int{50}, []int{}},
		{"Merge pull request #123 from acme/jdoe/x\n\nbody", []int{123}, []int{}},
		{"Merged PR 44946: fix: regex\n\nRelated work items: #1", []int{}, []int{44946}},
		{"squash (#5)\n\n* first (#6)\n* second", []int{5, 6}, []int{}},
		{"mentions #7 in passing", []int{}, []int{}},
	}
	for _, tt := range tests {
		if got := prNumbersInMessage(tt.message); !reflect.DeepEqual(got, tt.github) {
			t.Errorf("prNumbersInMessage(%q) = %v, want %v", tt.message, got, tt.github)
		}
		if got := adoPRNumbersInMessage(tt.message); !reflect.DeepEqual(got, tt.ado) {
			t.Errorf("adoPRNumbersInMessage(%q) = %v, want %v", tt.message, got, tt.ado)
		}
	}
}
