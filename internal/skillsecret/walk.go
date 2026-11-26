// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillsecret

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	toolsKey    = "tools"
	authKey     = "auth"
	headersKey  = "headers"
	envKey      = "env"
	outsideTool = -1
)

var toolIDKeys = []string{"tool_name", "name", "group_name"}

type scalarRef struct {
	node       *yaml.Node
	path       string
	rel        string
	leaf       string
	key        string
	toolID     string
	entry      int
	inConfig   bool
	mapKind    string
	headerName string
	argSecret  bool
	argPrefix  string
	argFlag    string
	inArgs     bool
}

func (r scalarRef) value() string { return r.node.Value }

func (r scalarRef) shortTag() string { return r.node.ShortTag() }

func walkScalars(doc *yaml.Node, visit func(scalarRef)) {
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i].Value, root.Content[i+1]
		if key == toolsKey && val.Kind == yaml.SequenceNode {
			walkTools(val, visit)
			continue
		}
		walk(val, scalarRef{path: key, rel: key, leaf: key, key: key, entry: outsideTool}, visit)
	}
}

func walkTools(seq *yaml.Node, visit func(scalarRef)) {
	for i, entry := range seq.Content {
		if entry.Kind != yaml.MappingNode {
			continue
		}
		id := toolID(entry, i)
		base := toolsKey + "[" + id + "]"
		for j := 0; j+1 < len(entry.Content); j += 2 {
			key := entry.Content[j].Value
			walk(entry.Content[j+1], scalarRef{
				path:     base + "." + key,
				rel:      key,
				leaf:     key,
				key:      key,
				toolID:   id,
				entry:    i,
				inConfig: isToolConfigKey(key),
				mapKind:  mapKindOf(key, ""),
			}, visit)
		}
	}
}

func mapKindOf(key, inherited string) string {
	switch lower := strings.ToLower(key); lower {
	case authKey, headersKey:
		return lower
	case "environment_variables", envKey:
		return envKey
	}
	return inherited
}

func toolID(entry *yaml.Node, index int) string {
	if name := toolName(entry); name != "" {
		return name
	}
	return strconv.Itoa(index)
}

func toolName(entry *yaml.Node) string {
	for _, want := range toolIDKeys {
		if v := scalarAt(entry, want); v != nil && v.Value != "" {
			return v.Value
		}
	}
	return ""
}

func scalarAt(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == key && m.Content[j+1].Kind == yaml.ScalarNode {
			return m.Content[j+1]
		}
	}
	return nil
}

func nodeAt(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == key {
			return m.Content[j+1]
		}
	}
	return nil
}

func isToolConfigKey(key string) bool {
	for _, k := range toolConfigKeys {
		if k == key {
			return true
		}
	}
	return false
}

func walk(n *yaml.Node, st scalarRef, visit func(scalarRef)) {
	switch n.Kind {
	case yaml.ScalarNode:
		st.node = n
		visit(st)
	case yaml.SequenceNode:
		isArgs := strings.EqualFold(st.key, "args")
		for i, c := range n.Content {
			idx := "[" + strconv.Itoa(i) + "]"
			child := st
			child.path += idx
			child.rel += idx
			child.leaf += idx
			child.argSecret, child.argPrefix, child.argFlag, child.inArgs = false, "", "", false
			if isArgs && c.Kind == yaml.ScalarNode {
				child.inArgs = true
				child.argSecret, child.argPrefix, child.argFlag = argRedaction(n.Content, i)
			}
			walk(c, child, visit)
		}
	case yaml.MappingNode:
		headerName := ""
		if st.mapKind == headersKey && nodeAt(n, "value") != nil {
			if name := scalarAt(n, "name"); name != nil {
				headerName = name.Value
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			child := st
			child.path += "." + key
			child.rel += "." + key
			child.leaf = key
			child.key = key
			child.argSecret, child.argPrefix, child.argFlag, child.inArgs = false, "", "", false
			child.headerName = headerName
			child.mapKind = mapKindOf(key, st.mapKind)
			walk(n.Content[i+1], child, visit)
		}
	}
}

func setScalar(n *yaml.Node, value string, style yaml.Style, tag string) {
	n.Kind = yaml.ScalarNode
	n.Value = value
	n.Style = style
	n.Tag = tag
}

func sanitizeVarName(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	out := strings.TrimRight(b.String(), "_")
	if out == "" {
		return "VAR"
	}
	return out
}
