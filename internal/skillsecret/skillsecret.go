// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillsecret

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const RedactedSentinel = "<REDACTED>"

const RedactionRefusedMessage = "SKILL.md frontmatter could not be parsed for redaction"

func ToolConfigKeys() []string { return slices.Clone(toolConfigKeys) }

var toolConfigKeys = []string{
	"tool_config",
	"connection_params",
	"environment_variables",
	"manifest_file",
	"auth",
	"headers",
	"specification_url",
	"specification_file",
	"base_url",
}

type Substitution struct {
	FieldPath   string
	Placeholder string
}

func Redact(skillMD []byte) (out []byte, redacted []string, err error) {
	s, doc, err := parseSkillFrontMatter(skillMD)
	if err != nil {
		return nil, nil, err
	}
	if doc == nil {
		return skillMD, nil, nil
	}
	walkScalars(doc, func(r scalarRef) {
		if replacement, ok := redactionFor(r); ok {
			setScalar(r.node, replacement, yaml.DoubleQuotedStyle, "!!str")
			redacted = append(redacted, r.path)
		}
	})
	if len(redacted) == 0 {
		return skillMD, nil, nil
	}
	out, err = s.reassemble(doc)
	if err != nil {
		return nil, nil, err
	}
	return out, redacted, nil
}

func RedactNested(skillMD []byte) (out []byte, redacted []string, err error) {
	out, redacted, err = Redact(skillMD)
	if err != nil {
		return skillMD, nil, nil
	}
	return out, redacted, nil
}

func Restore(incoming, stored []byte) (out []byte, unresolved []string, err error) {
	if !bytes.Contains(incoming, []byte(RedactedSentinel)) {
		return incoming, nil, nil
	}
	s, doc, err := parseSkillFrontMatter(incoming)
	if err != nil {
		return nil, nil, err
	}
	if doc == nil {
		return incoming, nil, nil
	}

	storedEntries, storedRefs := storedTools(stored)
	matcher := newEntryMatcher(storedEntries, storedRefs)
	resolved := map[*yaml.Node]*yaml.Node{}
	needsRestore := map[int]bool{}
	walkScalars(doc, func(r scalarRef) {
		if _, redacted := sentinelOf(r); redacted && r.entry != outsideTool {
			needsRestore[r.entry] = true
		}
	})
	entries := toolEntries(doc)
	for i, entry := range entries {
		if entry == nil || !needsRestore[i] {
			continue
		}
		maps.Copy(resolved, matcher.match(entry))
		if matcher.exceeded {
			resolved = map[*yaml.Node]*yaml.Node{}
			break
		}
	}

	restored := false
	walkScalars(doc, func(r scalarRef) {
		if _, redacted := sentinelOf(r); !redacted {
			return
		}
		prev, found := resolved[r.node]
		if !found || r.entry == outsideTool || !r.inConfig {
			unresolved = append(unresolved, r.path)
			return
		}
		setScalar(r.node, prev.Value, prev.Style, prev.Tag)
		restored = true
	})
	if !restored {
		return incoming, unresolved, nil
	}
	out, err = s.reassemble(doc)
	if err != nil {
		return nil, nil, err
	}
	return out, unresolved, nil
}

func toolEntries(doc *yaml.Node) []*yaml.Node {
	seq := nodeAt(doc.Content[0], toolsKey)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]*yaml.Node, len(seq.Content))
	for i, e := range seq.Content {
		if e.Kind == yaml.MappingNode {
			out[i] = e
		}
	}
	return out
}

func storedTools(stored []byte) ([]*yaml.Node, map[*yaml.Node]scalarRef) {
	s, ok := splitFrontMatter(stored)
	if !ok {
		return nil, nil
	}
	doc, err := parseFrontMatter(s)
	if err != nil || doc == nil {
		return nil, nil
	}
	refs := map[*yaml.Node]scalarRef{}
	walkScalars(doc, func(r scalarRef) { refs[r.node] = r })
	return toolEntries(doc), refs
}

func Placeholders(skillMD []byte, skillName string) (out []byte, subs []Substitution, err error) {
	return PlaceholdersShared(skillMD, skillName, map[string]int{})
}

func PlaceholdersShared(skillMD []byte, skillName string, used map[string]int) (out []byte, subs []Substitution, err error) {
	if !bytes.Contains(skillMD, []byte(RedactedSentinel)) {
		return skillMD, nil, nil
	}
	s, doc, err := parseSkillFrontMatter(skillMD)
	if err != nil {
		return nil, nil, err
	}
	if doc == nil {
		return skillMD, nil, nil
	}
	walkScalars(doc, func(r scalarRef) {
		prefix, redacted := sentinelOf(r)
		if !redacted {
			return
		}
		placeholder := "${" + placeholderName(skillName, r, prefix, used) + "}"
		setScalar(r.node, prefix+placeholder, yaml.SingleQuotedStyle, "!!str")
		subs = append(subs, Substitution{FieldPath: r.path, Placeholder: placeholder})
	})
	if len(subs) == 0 {
		return skillMD, nil, nil
	}
	out, err = s.reassemble(doc)
	if err != nil {
		return nil, nil, err
	}
	return out, subs, nil
}

func placeholderName(skillName string, r scalarRef, prefix string, used map[string]int) string {
	parts := []string{"SKILL", skillName}
	if r.toolID != "" {
		parts = append(parts, r.toolID)
	}
	if prefix != "" {
		parts = append(parts, prefixName(prefix))
	} else {
		parts = append(parts, r.leaf)
	}
	name := sanitizeVarName(strings.Join(parts, "_"))
	candidate := name
	for n := 2; used[candidate] > 0; n++ {
		candidate = fmt.Sprintf("%s_%d", name, n)
	}
	used[candidate]++
	return candidate
}
