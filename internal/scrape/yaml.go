package scrape

import (
	"errors"
	"io"
	"math"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxYAMLDepth = 100

// parseYAMLDocs returns the mapping documents of a (multi-document) YAML file, in order. Like the
// raw data it feeds, values stay as written: key order is kept, and timestamps stay text.
func parseYAMLDocs(text string) ([]*orderedMap, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var docs []*orderedMap
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
		if m, ok := v.(*orderedMap); ok {
			docs = append(docs, m)
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
func yamlMapping(n *yaml.Node, depth int) (*orderedMap, error) {
	isMerge := func(k *yaml.Node) bool { return k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" }
	explicit := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !isMerge(n.Content[i]) {
			explicit[yamlKey(n.Content[i])] = true
		}
	}
	m := newOrderedMap()
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !isMerge(k) {
			val, err := yamlValue(v, depth+1)
			if err != nil {
				return nil, err
			}
			m.set(yamlKey(k), val)
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
			if mm, ok := merged.(*orderedMap); ok {
				for _, key := range mm.keys {
					if _, set := m.values[key]; !set && !explicit[key] {
						m.set(key, mm.values[key])
					}
				}
			}
		}
	}
	return m, nil
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
		if b, err := marshal(v); err == nil {
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
