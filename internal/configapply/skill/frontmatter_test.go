// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStampVersion_WritesUnderMetadata(t *testing.T) {
	in := "---\nname: sam-operate\ndescription: x\n---\n\n# body\n"
	out, err := StampVersion([]byte(in), "v2.126.3")
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: sam-operate\ndescription: x\nmetadata:\n  version: v2.126.3\n---\n\n# body\n"
	if string(out) != want {
		t.Fatalf("stamped output:\n%s\nwant:\n%s", out, want)
	}
}

func TestStampVersion_ReplacesLegacyTopLevelAndEarlierStamp(t *testing.T) {
	in := "---\nname: x\nversion: old\nmetadata:\n  version: older\n  owner: sam\n---\nbody\n"
	out, err := StampVersion([]byte(in), "new")
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: x\nmetadata:\n  owner: sam\n  version: new\n---\nbody\n"
	if string(out) != want {
		t.Fatalf("stamped output:\n%s\nwant:\n%s", out, want)
	}
}

func TestStampVersion_PreservesEveryOtherFrontmatterLine(t *testing.T) {
	description := `description: Use when someone says "I want an assistant that…" — covers agents, workflows (switch/map/loop), and the sam config CLI. Not for troubleshooting.`
	in := "---\nname: sam-concierge\n" + description + "\nallowed-tools: Bash Read\n---\n# body\n"
	out, err := StampVersion([]byte(in), "v1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\n"+description+"\n") {
		t.Fatalf("description line was rewritten:\n%s", out)
	}
	var parsed map[string]any
	block, _, err := splitFrontmatter(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(block, &parsed); err != nil {
		t.Fatalf("stamped frontmatter is not valid YAML: %v\n%s", err, out)
	}
	if got := parsed["allowed-tools"]; got != "Bash Read" {
		t.Errorf("allowed-tools = %v, want preserved", got)
	}
	if got := parsed["metadata"].(map[string]any)["version"]; got != "v1" {
		t.Errorf("metadata.version = %v, want v1", got)
	}
}

func TestStampVersion_RejectsBodiesWithoutFrontmatter(t *testing.T) {
	for name, body := range map[string]string{
		"missing":      "# no frontmatter\n",
		"unterminated": "---\nname: x\nbody never closes\n",
	} {
		if _, err := StampVersion([]byte(body), "v1"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseFrontmatter_ReadsVersionFromMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	body := "---\nname: sam-x\ndescription: d\nmetadata:\n  version: 2.348.19\n---\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fm, err := ParseFrontmatter(path)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "sam-x" || fm.Version() != "2.348.19" {
		t.Errorf("parsed %+v, want name sam-x and version 2.348.19", fm)
	}
}

func TestParseFrontmatter_LegacyTopLevelVersionReadsAsUnstamped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: sam-x\nversion: 1.0\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fm, err := ParseFrontmatter(path)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Version() != "" {
		t.Errorf("version = %q, want unstamped", fm.Version())
	}
}

func TestParseFrontmatter_ToleratesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	body := "---\r\nname: sam-operate\r\ndescription: d\r\nmetadata:\r\n  version: 2.348.19\r\n---\r\n\r\n# body\r\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fm, err := ParseFrontmatter(path)
	if err != nil {
		t.Fatalf("CRLF SKILL.md failed to parse: %v", err)
	}
	if fm.Name != "sam-operate" || fm.Version() != "2.348.19" {
		t.Errorf("parsed %+v, want name sam-operate and version 2.348.19", fm)
	}
}

func TestSplitFrontmatter_BothDelimitersTolerateCRLFAndTrailingSpace(t *testing.T) {
	for name, body := range map[string]string{
		"crlf":         "---\r\nname: x\r\n---\r\nbody\r\n",
		"crlf opening": "---\r\nname: x\n---\nbody\n",
		"crlf closing": "---\nname: x\n---\r\nbody\n",
		"trailing tab": "---\t\nname: x\n--- \nbody\n",
	} {
		t.Run(name, func(t *testing.T) {
			block, _, err := splitFrontmatter([]byte(body))
			if err != nil {
				t.Fatalf("splitFrontmatter: %v", err)
			}
			var fm Frontmatter
			if err := yaml.Unmarshal(block, &fm); err != nil {
				t.Fatalf("block is not valid YAML: %v\n%q", err, block)
			}
			if fm.Name != "x" {
				t.Errorf("name = %q, want x", fm.Name)
			}
		})
	}
}
