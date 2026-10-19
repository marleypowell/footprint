// Package doc holds raw documents: JSON-ready values kept as their authors wrote them. Objects
// keep their key order, and YAML scalars other than null, bool, int and float keep their text.
package doc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Object is a JSON object that keeps insertion order (encoding/json sorts map keys).
type Object struct {
	keys   []string
	values map[string]any
}

func NewObject() *Object { return &Object{values: map[string]any{}} }

// Set adds or replaces a key; a replaced key keeps its original position.
func (o *Object) Set(key string, value any) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *Object) Get(key string) (any, bool) {
	v, ok := o.values[key]
	return v, ok
}

func (o *Object) Keys() []string { return slices.Clone(o.keys) }

func (o *Object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// UnmarshalJSON decodes a JSON object keeping its key order, nested objects included (as *Object).
func (o *Object) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	v, err := decodeJSON(dec)
	if err != nil {
		return err
	}
	obj, ok := v.(*Object)
	if !ok {
		return fmt.Errorf("doc: want a JSON object, got %T", v)
	}
	*o = *obj
	return nil
}

// decodeJSON reads one value: objects as *Object, arrays as []any, scalars as encoding/json does.
func decodeJSON(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		obj := NewObject()
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeJSON(dec)
			if err != nil {
				return nil, err
			}
			obj.Set(key.(string), v)
		}
		_, err := dec.Token() // }
		return obj, err
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := decodeJSON(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token() // ]
		return arr, err
	}
	return nil, fmt.Errorf("doc: unexpected %v", delim)
}

// Marshal encodes v as compact JSON without HTML escaping: the output is data, not web content.
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Field returns obj[key] for a decoded JSON or YAML object, and nil for anything else, so lookups
// can chain through missing or mistyped values.
func Field(obj any, key string) any {
	switch o := obj.(type) {
	case *Object:
		if o != nil {
			return o.values[key]
		}
	case map[string]any:
		return o[key]
	}
	return nil
}

// IsObject reports whether v is a decoded JSON or YAML object.
func IsObject(v any) bool {
	switch o := v.(type) {
	case *Object:
		return o != nil
	case map[string]any:
		return true
	}
	return false
}

// String renders a scalar as text; nil is "".
func String(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	default:
		return fmt.Sprint(s)
	}
}

const maxYAMLDepth = 100

// ParseYAML returns the mapping documents of a (multi-document) YAML file, in order.
func ParseYAML(text string) ([]*Object, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var docs []*Object
	for {
		var node yaml.Node
		err := dec.Decode(&node)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		v, err := yamlValue(&node, 0)
		if err != nil {
			return nil, err
		}
		if o, ok := v.(*Object); ok {
			docs = append(docs, o)
		}
	}
}

func yamlValue(n *yaml.Node, depth int) (any, error) {
	if depth > maxYAMLDepth {
		return nil, errors.New("YAML nested too deeply (alias cycle?)")
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return yamlValue(n.Content[0], depth+1)
	case yaml.AliasNode:
		return yamlValue(n.Alias, depth+1)
	case yaml.SequenceNode:
		items := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := yamlValue(c, depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		return items, nil
	case yaml.MappingNode:
		return yamlMapping(n, depth)
	case yaml.ScalarNode:
		return yamlScalar(n), nil
	}
	return nil, nil
}

// yamlMapping keeps key order. A `<<` merge key contributes the merged mappings' keys that the
// mapping does not set itself.
func yamlMapping(n *yaml.Node, depth int) (*Object, error) {
	isMerge := func(k *yaml.Node) bool { return k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" }
	explicit := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !isMerge(n.Content[i]) {
			explicit[yamlKey(n.Content[i])] = true
		}
	}
	o := NewObject()
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !isMerge(k) {
			val, err := yamlValue(v, depth+1)
			if err != nil {
				return nil, err
			}
			o.Set(yamlKey(k), val)
			continue
		}
		sources := []*yaml.Node{v}
		if target := unalias(v); target.Kind == yaml.SequenceNode {
			sources = target.Content
		}
		for _, src := range sources {
			merged, err := yamlValue(src, depth+1)
			if err != nil {
				return nil, err
			}
			if mo, ok := merged.(*Object); ok {
				for _, key := range mo.keys {
					if _, set := o.values[key]; !set && !explicit[key] {
						o.Set(key, mo.values[key])
					}
				}
			}
		}
	}
	return o, nil
}

func unalias(n *yaml.Node) *yaml.Node {
	for i := 0; n.Kind == yaml.AliasNode && n.Alias != nil && i < maxYAMLDepth; i++ {
		n = n.Alias
	}
	return n
}

func yamlKey(k *yaml.Node) string {
	k = unalias(k)
	if k.Kind == yaml.ScalarNode {
		return k.Value
	}
	if v, err := yamlValue(k, 0); err == nil {
		if b, err := Marshal(v); err == nil {
			return string(b)
		}
	}
	return k.Value
}

// yamlScalar decodes null, bool, int and float scalars to JSON types; everything else (strings,
// timestamps, binary, custom tags) keeps its text.
func yamlScalar(n *yaml.Node) any {
	switch n.ShortTag() {
	case "!!null":
		return nil
	case "!!bool", "!!int", "!!float":
		var v any
		if err := n.Decode(&v); err != nil {
			return n.Value
		}
		if f, ok := v.(float64); ok && (math.IsInf(f, 0) || math.IsNaN(f)) {
			return n.Value // not representable in JSON
		}
		return v
	}
	return n.Value
}
