package doc

import "testing"

func TestParseYAML(t *testing.T) {
	text := `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: checkout-ui
  description: Takes orders
spec:
  type: website
  created: 2023-01-01
  replicas: 2
  ratio: 0.5
  enabled: true
  missing: ~
---
- not
- a mapping
---
base: &base {a: 1, b: 2}
child:
  <<: *base
  b: 3
`
	docs, err := ParseYAML(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d mapping documents, want 2", len(docs))
	}
	want := `{"apiVersion":"backstage.io/v1alpha1","kind":"Component","metadata":{"name":"checkout-ui",` +
		`"description":"Takes orders"},"spec":{"type":"website","created":"2023-01-01","replicas":2,` +
		`"ratio":0.5,"enabled":true,"missing":null}}`
	if got := marshal(t, docs[0]); got != want {
		t.Errorf("key order, timestamps as text, scalar types:\n got %s\nwant %s", got, want)
	}
	if got := marshal(t, Field(docs[1], "child")); got != `{"a":1,"b":3}` {
		t.Errorf("merge key: got %s", got)
	}
	if _, err := ParseYAML("key: [unclosed"); err == nil {
		t.Error("invalid YAML should fail")
	}
}

func TestObjectAndField(t *testing.T) {
	o := NewObject()
	o.Set("b", 1)
	o.Set("a", 2)
	o.Set("b", 3) // replacing keeps the original position
	if got := marshal(t, o); got != `{"b":3,"a":2}` {
		t.Errorf("got %s", got)
	}
	decoded := map[string]any{"metadata": map[string]any{"name": "x"}}
	if Field(Field(decoded, "metadata"), "name") != "x" || Field(Field(o, "missing"), "name") != nil {
		t.Error("Field should chain through decoded maps and missing values")
	}
	if String(nil) != "" || String(42) != "42" || !IsObject(o) || IsObject("x") {
		t.Error("String/IsObject")
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
