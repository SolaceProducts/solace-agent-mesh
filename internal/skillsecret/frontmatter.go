// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillsecret

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const frontMatterDelimiter = "---"

const mergeKeyTag = "!!merge"

var (
	utf8BOM = []byte{0xEF, 0xBB, 0xBF}

	errUnclosedFrontMatter = errors.New("front matter has no closing delimiter")
	errYAMLReuse           = errors.New("front matter uses YAML anchors, aliases or merge keys")
)

type splitDoc struct {
	head   []byte
	yaml   []byte
	tail   []byte
	crlf   bool
	closed bool
}

func splitFrontMatter(doc []byte) (splitDoc, bool) {
	rest := bytes.TrimPrefix(doc, utf8BOM)
	if !bytes.HasPrefix(rest, []byte(frontMatterDelimiter)) {
		return splitDoc{}, false
	}
	headLen := len(doc) - len(rest)
	lineEnd := bytes.IndexByte(rest, '\n')
	if lineEnd < 0 {
		return splitDoc{}, false
	}
	headLen += lineEnd + 1
	s := splitDoc{
		head: doc[:headLen],
		crlf: lineEnd > 0 && rest[lineEnd-1] == '\r',
	}

	pos := headLen
	for pos < len(doc) {
		next := bytes.IndexByte(doc[pos:], '\n')
		end := len(doc)
		if next >= 0 {
			end = pos + next + 1
		}
		line := strings.TrimSuffix(strings.TrimSuffix(string(doc[pos:end]), "\n"), "\r")
		if line == frontMatterDelimiter {
			s.yaml = doc[headLen:pos]
			s.tail = doc[pos:]
			s.closed = true
			return s, true
		}
		pos = end
	}
	s.yaml = doc[headLen:]
	return s, true
}

func parseSkillFrontMatter(skillMD []byte) (splitDoc, *yaml.Node, error) {
	s, ok := splitFrontMatter(skillMD)
	if !ok {
		return s, nil, nil
	}
	doc, err := parseFrontMatter(s)
	if err != nil {
		return s, nil, fmt.Errorf("parse SKILL.md front matter: %w", err)
	}
	return s, doc, nil
}

func parseFrontMatter(s splitDoc) (*yaml.Node, error) {
	if !s.closed {
		return nil, errUnclosedFrontMatter
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(s.yaml, &doc); err != nil {
		return nil, err
	}
	if reusesNodes(&doc) {
		return nil, errYAMLReuse
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	return &doc, nil
}

func reusesNodes(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return true
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].ShortTag() == mergeKeyTag {
				return true
			}
		}
	}
	for _, c := range n.Content {
		if reusesNodes(c) {
			return true
		}
	}
	return false
}

func (s splitDoc) reassemble(doc *yaml.Node) ([]byte, error) {
	body, err := encodeYAML(doc)
	if err != nil {
		return nil, fmt.Errorf("encode SKILL.md front matter: %w", err)
	}
	if s.crlf {
		body = bytes.ReplaceAll(body, []byte("\n"), []byte("\r\n"))
	}
	out := make([]byte, 0, len(s.head)+len(body)+len(s.tail))
	out = append(out, s.head...)
	out = append(out, body...)
	out = append(out, s.tail...)
	return out, nil
}

func encodeYAML(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
