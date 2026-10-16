package scrape

import (
	"regexp"
	"sort"
	"strconv"
)

// adoPRInMessage finds Azure DevOps-era merges ("Merged PR 12345: title") in history migrated
// to the current code host. Those numbers are not the current host's change requests.
var adoPRInMessage = regexp.MustCompile(`(?m)^Merged PR (\d+):`)

func adoPRNumbersInMessage(message string) []int {
	seen := map[int]bool{}
	for _, m := range adoPRInMessage.FindAllStringSubmatch(message, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			seen[n] = true
		}
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}
