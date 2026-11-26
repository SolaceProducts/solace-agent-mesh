// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

type Frontmatter struct {
	Name     string            `yaml:"name"`
	Metadata map[string]string `yaml:"metadata"`
}

const versionMetadataKey = "version"

func (fm Frontmatter) Version() string { return fm.Metadata[versionMetadataKey] }

const frontmatterDelimiter = "---"

var topLevelKeyLine = regexp.MustCompile(`^([A-Za-z0-9_-]+):`)

func ParseFrontmatter(path string) (Frontmatter, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Frontmatter{}, err
	}
	block, _, err := splitFrontmatter(body)
	if err != nil {
		return Frontmatter{}, fmt.Errorf("%s: %w", path, err)
	}
	var fm Frontmatter
	if err := yaml.Unmarshal(block, &fm); err != nil {
		return Frontmatter{}, fmt.Errorf("%s: parse frontmatter: %w", path, err)
	}
	return fm, nil
}

func StampVersion(body []byte, version string) ([]byte, error) {
	block, rest, err := splitFrontmatter(body)
	if err != nil {
		return nil, err
	}
	var fm Frontmatter
	if parseErr := yaml.Unmarshal(block, &fm); parseErr != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", parseErr)
	}
	metadata := maps.Clone(fm.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata[versionMetadataKey] = version
	metadataBlock, err := renderMetadata(metadata)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.WriteString(frontmatterDelimiter + "\n")
	kept := dropTopLevelKeys(block, versionMetadataKey, "metadata")
	if len(kept) > 0 {
		out.Write(kept)
		if kept[len(kept)-1] != '\n' {
			out.WriteByte('\n')
		}
	}
	out.Write(metadataBlock)
	out.WriteString(frontmatterDelimiter + "\n")
	out.Write(rest)
	return out.Bytes(), nil
}

func splitFrontmatter(body []byte) (block, rest []byte, err error) {
	openingLine, blockStart := lineAt(body, 0)
	if !isFrontmatterDelimiter(openingLine) {
		return nil, nil, fmt.Errorf("missing frontmatter (expected leading %q)", frontmatterDelimiter)
	}
	afterOpening := body[blockStart:]
	offset := 0
	for offset < len(afterOpening) {
		line, next := lineAt(afterOpening, offset)
		if isFrontmatterDelimiter(line) {
			return afterOpening[:offset], afterOpening[next:], nil
		}
		offset = next
	}
	return nil, nil, fmt.Errorf("unterminated frontmatter (missing closing %q)", frontmatterDelimiter)
}

func lineAt(b []byte, offset int) (line []byte, next int) {
	if end := bytes.IndexByte(b[offset:], '\n'); end >= 0 {
		return b[offset : offset+end], offset + end + 1
	}
	return b[offset:], len(b)
}

func isFrontmatterDelimiter(line []byte) bool {
	return string(bytes.TrimRight(line, " \t\r")) == frontmatterDelimiter
}

func dropTopLevelKeys(block []byte, keys ...string) []byte {
	if len(bytes.TrimSpace(block)) == 0 {
		return nil
	}
	drop := map[string]bool{}
	for _, k := range keys {
		drop[k] = true
	}
	var kept [][]byte
	dropping := false
	for line := range bytes.SplitSeq(bytes.TrimSuffix(block, []byte("\n")), []byte("\n")) {
		if m := topLevelKeyLine.FindSubmatch(line); m != nil {
			dropping = drop[string(m[1])]
		} else if len(bytes.TrimSpace(line)) > 0 && !isIndented(line) {
			dropping = false
		}
		if !dropping {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return append(bytes.Join(kept, []byte("\n")), '\n')
}

func isIndented(line []byte) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

func renderMetadata(metadata map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	doc := struct {
		Metadata map[string]string `yaml:"metadata"`
	}{Metadata: metadata}
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("render metadata: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("render metadata: %w", err)
	}
	return buf.Bytes(), nil
}
