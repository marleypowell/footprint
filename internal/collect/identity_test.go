package collect

import (
	"reflect"
	"testing"
)

func testIdentity(t *testing.T) *identity {
	t.Helper()
	id, err := newIdentity(&Config{
		Login: "jdoe",
		Identity: IdentityConfig{
			Emails:           []string{"jane.doe@example.com", "jane@personal.example", "12345+jdoe@users.noreply.github.com"},
			Names:            []string{"Jane Doe", "jdoe"},
			DiscoveryPattern: "jane|doe",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIdentityMatch(t *testing.T) {
	id := testIdentity(t)
	tests := []struct{ name, email, want string }{
		{"Jane", "Jane.Doe@Example.com", "email"},
		{"jdoe", "12345+JDOE@users.noreply.github.com", "email"},
		{"someone", "jdoe@users.noreply.github.com", ""}, // noreply forms count only when configured
		{"jane doe", "laptop@localhost", "name"},
		{"Jane", "laptop@localhost", ""},
		{"Other Person", "other@example.com", ""},
	}
	for _, tt := range tests {
		if got := id.match(tt.name, tt.email); got != tt.want {
			t.Errorf("match(%q, %q) = %q, want %q", tt.name, tt.email, got, tt.want)
		}
	}
	if !id.resembles("Janet Smith", "js@example.com") || id.resembles("Other Person", "other@example.com") {
		t.Error("resembles should follow the discovery pattern")
	}
}

func TestParseCoauthors(t *testing.T) {
	msg := "feat: thing\n\nbody\n\nCo-authored-by: Jane Doe <jane.doe@example.com>\n" +
		"co-authored-by:   Claude <noreply@anthropic.com>  \nCo-Authored-By: Just A Name"
	want := []Person{
		{Name: "Jane Doe", Email: "jane.doe@example.com"},
		{Name: "Claude", Email: "noreply@anthropic.com"},
		{Name: "Just A Name"},
	}
	if got := parseCoauthors(msg); !reflect.DeepEqual(got, want) {
		t.Errorf("parseCoauthors = %+v, want %+v", got, want)
	}
	if got := parseCoauthors("no trailers"); got == nil || len(got) != 0 {
		t.Errorf("want an empty, non-nil slice, got %#v", got)
	}
}
