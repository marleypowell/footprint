package catalog

import "testing"

// Systems, components, APIs and resources are software; domains and groups are how an org is
// organised, and "other" is the catalog's bookkeeping. Categories are the neutral lowercase names
// adapters map onto, so a catalog's own kind name (or anything else) is not software.
func TestDescribesSoftware(t *testing.T) {
	tests := []struct {
		category string
		want     bool
	}{
		{System, true},
		{Component, true},
		{API, true},
		{Resource, true},
		{Domain, false},
		{Group, false},
		{Other, false},
		{"Component", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := DescribesSoftware(tt.category); got != tt.want {
			t.Errorf("DescribesSoftware(%q) = %v, want %v", tt.category, got, tt.want)
		}
	}
}
