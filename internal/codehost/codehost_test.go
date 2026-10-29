package codehost

import "testing"

// GitHub reports no default branch for an empty repository, so DefaultBranch is nil there and
// DefaultBranchName gives "" rather than dereferencing it.
func TestDefaultBranchName(t *testing.T) {
	main := "main"
	tests := []struct {
		name string
		repo Repo
		want string
	}{
		{"a repository with commits", Repo{Name: "pay", DefaultBranch: &main}, "main"},
		{"an empty repository", Repo{Name: "scratch", IsEmpty: true}, ""},
	}
	for _, tt := range tests {
		if got := tt.repo.DefaultBranchName(); got != tt.want {
			t.Errorf("%s: DefaultBranchName() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
