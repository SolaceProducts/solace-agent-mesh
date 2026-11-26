// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillsecret

import "gopkg.in/yaml.v3"

const maxMatchWork = 1_000_000

type redaction struct {
	replacement string
	redacted    bool
}

type entryMatcher struct {
	buckets    map[string][]*yaml.Node
	refs       map[*yaml.Node]scalarRef
	keyIndexes map[*yaml.Node]map[string]*yaml.Node
	redactions map[*yaml.Node]redaction
	work       int
	exceeded   bool
}

func newEntryMatcher(stored []*yaml.Node, refs map[*yaml.Node]scalarRef) *entryMatcher {
	m := &entryMatcher{
		buckets:    map[string][]*yaml.Node{},
		refs:       refs,
		keyIndexes: map[*yaml.Node]map[string]*yaml.Node{},
		redactions: map[*yaml.Node]redaction{},
	}
	for _, entry := range stored {
		if entry != nil {
			id := toolIdentity(entry)
			m.buckets[id] = append(m.buckets[id], entry)
		}
	}
	return m
}

func toolIdentity(entry *yaml.Node) string {
	toolType := ""
	if v := scalarAt(entry, "tool_type"); v != nil {
		toolType = v.Value
	}
	return toolType + "\x00" + toolName(entry)
}

func (m *entryMatcher) match(incoming *yaml.Node) map[*yaml.Node]*yaml.Node {
	var found map[*yaml.Node]*yaml.Node
	matches := 0
	for _, candidate := range m.buckets[toolIdentity(incoming)] {
		pairs := map[*yaml.Node]*yaml.Node{}
		if m.same(incoming, candidate, pairs) {
			matches++
			found = pairs
		}
		if m.exceeded {
			return nil
		}
	}
	if matches != 1 {
		return nil
	}
	return found
}

func (m *entryMatcher) redactionOf(stored *yaml.Node) redaction {
	r, ok := m.redactions[stored]
	if !ok {
		r.replacement, r.redacted = redactionFor(m.refs[stored])
		m.redactions[stored] = r
	}
	return r
}

func (m *entryMatcher) charge(n int) bool {
	m.work += n
	if m.work > maxMatchWork {
		m.exceeded = true
	}
	return !m.exceeded
}

func (m *entryMatcher) keysOf(node *yaml.Node) map[string]*yaml.Node {
	idx, ok := m.keyIndexes[node]
	if !ok {
		m.charge(len(node.Content) / 2)
		idx = indexKeys(node)
		m.keyIndexes[node] = idx
	}
	return idx
}

func (m *entryMatcher) same(incoming, stored *yaml.Node, pairs map[*yaml.Node]*yaml.Node) bool {
	if !m.charge(1) || incoming.Kind != stored.Kind {
		return false
	}
	switch incoming.Kind {
	case yaml.ScalarNode:
		if r := m.redactionOf(stored); r.redacted {
			if incoming.Value != r.replacement {
				return false
			}
			pairs[incoming] = stored
			return true
		}
		return incoming.Value == stored.Value && incoming.ShortTag() == stored.ShortTag()
	case yaml.SequenceNode:
		if len(incoming.Content) != len(stored.Content) {
			return false
		}
		for i := range incoming.Content {
			if !m.same(incoming.Content[i], stored.Content[i], pairs) {
				return false
			}
		}
		return true
	case yaml.MappingNode:
		if len(incoming.Content) != len(stored.Content) {
			return false
		}
		storedKeys, incomingKeys := m.keysOf(stored), m.keysOf(incoming)
		if incomingKeys == nil || storedKeys == nil || m.exceeded {
			return false
		}
		for i := 0; i+1 < len(stored.Content); i += 2 {
			key := stored.Content[i].Value
			value, ok := incomingKeys[key]
			if !ok || !m.same(value, stored.Content[i+1], pairs) {
				return false
			}
		}
		return true
	}
	return false
}

func indexKeys(m *yaml.Node) map[string]*yaml.Node {
	keys := make(map[string]*yaml.Node, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		if _, dup := keys[m.Content[i].Value]; dup {
			return nil
		}
		keys[m.Content[i].Value] = m.Content[i+1]
	}
	return keys
}
