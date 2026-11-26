// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package design

import (
	"errors"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestSkills_ListsExpectedDesignSkills(t *testing.T) {
	got := Skills()
	want := []string{"sam-best-practices", "sam-concepts", "sam-agent-design", "sam-workflow-design", "sam-skill-design", "sam-efficiency"}
	if len(got) != len(want) {
		t.Fatalf("got %d skills, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("Skills()[%d].Name = %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestRouter_FrontmatterNamesBodyAndStaysSmall(t *testing.T) {
	const maxRouterBytes = 6000
	for _, sk := range Skills() {
		router, err := sk.Router()
		if err != nil {
			t.Fatalf("%s.Router(): %v", sk.Name, err)
		}
		text := string(router)
		if !strings.HasPrefix(text, "---\n") || !strings.Contains(text, "\n---\n") {
			t.Errorf("%s: router lacks a terminated frontmatter block", sk.Guide)
		}
		if !strings.Contains(text, sk.BodyFile) {
			t.Errorf("%s: router never names its body %s — the pointer is stale", sk.Guide, sk.BodyFile)
		}
		if len(router) > maxRouterBytes {
			t.Errorf("%s: router is %d bytes, over the %d-byte ceiling — move detail into shared/%s.md",
				sk.Guide, len(router), maxRouterBytes, sk.Guide)
		}
	}
}

func TestRouter_DoesNotDuplicateShared(t *testing.T) {
	for _, sk := range Skills() {
		router, err := sk.Router()
		if err != nil {
			t.Fatalf("%s.Router(): %v", sk.Name, err)
		}
		shared := readShared(t, sk)
		for para := range strings.SplitSeq(shared, "\n\n") {
			para = strings.TrimSpace(para)
			if len(para) < 200 {
				continue
			}
			if strings.Contains(string(router), para) {
				t.Errorf("%s: router duplicates a paragraph of the shared guide — index it, do not restate it:\n\t%.120s…", sk.Guide, para)
			}
		}
	}
}

func TestBody_EachReaderGetsTheGuideAndExactlyItsOwnSection(t *testing.T) {
	const minSharedBytes = 4000
	for _, sk := range Skills() {
		shared := readShared(t, sk)
		if len(shared) < minSharedBytes {
			t.Errorf("%s: shared guide is %d bytes, under the %d-byte floor — a guide this short is a truncation", sk.Guide, len(shared), minSharedBytes)
		}
		if strings.HasPrefix(shared, "---\n") {
			t.Errorf("%s: shared guide carries frontmatter, which renders as literal text in a reference", sk.Guide)
		}
		for _, c := range []Consumer{Builder, CLI} {
			if strings.Contains(shared, headingLine(c)) {
				t.Errorf("%s: shared guide hand-writes the %q heading; reader sections live in %s/", sk.Guide, c.SectionHeading(), c)
			}
			body, err := sk.Body(c)
			if err != nil {
				t.Fatalf("%s.Body(%s): %v", sk.Name, c, err)
			}
			text := string(body)
			if !strings.HasPrefix(text, shared) {
				t.Errorf("%s: Body(%s) does not start with the shared guide", sk.Guide, c)
			}
			if got := strings.Count(text, headingLine(c)); got != 1 {
				t.Errorf("%s: Body(%s) carries the %q heading %d times, want exactly 1", sk.Guide, c, c.SectionHeading(), got)
			}
			if other := otherConsumer(c); strings.Contains(text, headingLine(other)) {
				t.Errorf("%s: Body(%s) leaks the %q section", sk.Guide, c, other.SectionHeading())
			}
		}
	}
}

var (
	skillToken    = regexp.MustCompile("`sam-[a-z0-9-]+`")
	referencePath = regexp.MustCompile(`references/[A-Za-z0-9_-]+(\.md|/)`)
)

func TestShared_IsNeutral(t *testing.T) {
	for _, sk := range Skills() {
		for i, line := range proseLines(readShared(t, sk)) {
			if m := skillToken.FindString(line); m != "" {
				t.Errorf("shared/%s.md:%d names skill %s — a pointer belongs in builder/%s.md or cli/%s.md, the shared text says \"see the lookup table at the end of this guide\"",
					sk.Guide, i+1, m, sk.Guide, sk.Guide)
			}
			if m := referencePath.FindString(line); m != "" {
				t.Errorf("shared/%s.md:%d names file path %s — it exists only for the CLI reader; move it to cli/%s.md",
					sk.Guide, i+1, m, sk.Guide)
			}
		}
	}
}

func TestFS_LayoutIsClosed(t *testing.T) {
	guides := map[string]bool{}
	for _, sk := range Skills() {
		guides[sk.Guide] = true
		for _, p := range []string{sk.sharedPath(), sk.routerPath()} {
			if _, err := fs.Stat(FS(), p); err != nil {
				t.Errorf("%s: required file %s missing", sk.Guide, p)
			}
		}
	}
	err := fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		dir, base := path.Split(p)
		guide := strings.TrimSuffix(strings.TrimSuffix(base, ".md"), ".router")
		switch {
		case dir == "shared/" && strings.HasSuffix(base, ".md") && guides[guide]:
		case dir == "builder/" && strings.HasSuffix(base, ".md") && guides[guide]:
		case dir == "cli/" && strings.HasSuffix(base, ".md") && !strings.HasSuffix(base, ".router.md") && guides[guide]:
		default:
			t.Errorf("%q is not a shared guide, a router, or a reader section of a known guide", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readShared(t *testing.T, sk Skill) string {
	t.Helper()
	b, err := fs.ReadFile(FS(), sk.sharedPath())
	if err != nil {
		t.Fatalf("%s: read shared guide: %v", sk.Guide, err)
	}
	return string(b)
}

func headingLine(c Consumer) string { return "\n## " + c.SectionHeading() + "\n" }

func otherConsumer(c Consumer) Consumer {
	if c == Builder {
		return CLI
	}
	return Builder
}

func proseLines(text string) []string {
	lines := strings.Split(text, "\n")
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			lines[i] = ""
			continue
		}
		if inFence {
			lines[i] = ""
		}
	}
	return lines
}

func TestRouter_SectionNamesAreHeadingsInBuilderBody(t *testing.T) {
	for _, sk := range Skills() {
		router, err := sk.Router()
		if err != nil {
			t.Fatalf("%s.Router(): %v", sk.Name, err)
		}
		headings := headingTexts(readBody(t, sk, Builder))
		for i, line := range strings.Split(string(router), "\n") {
			cell, ok := routerSectionCell(line)
			if !ok {
				continue
			}
			for _, name := range splitSectionNames(cell) {
				if !slices.ContainsFunc(headings, func(h string) bool {
					return strings.Contains(strings.ToLower(h), strings.ToLower(name))
				}) {
					t.Errorf("%s.router.md:%d names section %q, which is not a heading in Body(Builder) — grep would miss it", sk.Guide, i+1, name)
				}
			}
		}
	}
}

func TestBody_SkillAttachmentSyntaxMatchesReader(t *testing.T) {
	const cliOnly, builderOnly = "skillRefs", "agent_card.skills"
	for _, sk := range Skills() {
		if strings.Contains(readBody(t, sk, Builder), cliOnly) {
			t.Errorf("%s: Body(Builder) says %q, the declarative field", sk.Guide, cliOnly)
		}
		if strings.Contains(readBody(t, sk, CLI), builderOnly) {
			t.Errorf("%s: Body(CLI) says %q, the runtime YAML path", sk.Guide, builderOnly)
		}
	}
	skillDesign := skillNamed(t, "sam-skill-design")
	if !strings.Contains(readBody(t, skillDesign, CLI), cliOnly) {
		t.Errorf("skill-design: Body(CLI) never says how to attach a skill (%q)", cliOnly)
	}
	if !strings.Contains(readBody(t, skillDesign, Builder), builderOnly) {
		t.Errorf("skill-design: Body(Builder) never tells %q apart from knowledge skills", builderOnly)
	}
}

func TestSectionHeading_UnknownConsumerIsDerived(t *testing.T) {
	if got, want := Consumer("mcp").SectionHeading(), "Working in mcp"; got != want {
		t.Errorf("SectionHeading() = %q, want %q", got, want)
	}
}

func TestBody_MissingGuideErrors(t *testing.T) {
	_, err := Skill{Name: "sam-missing", Guide: "missing"}.Body(Builder)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Body() error = %v, want a wrapped fs.ErrNotExist", err)
	}
}

func TestBody_ReaderWithoutSectionGetsSharedAlone(t *testing.T) {
	for _, sk := range Skills() {
		if got := readBody(t, sk, Consumer("nobody")); got != readShared(t, sk) {
			t.Errorf("%s: a reader with no section should receive the shared guide unchanged", sk.Guide)
		}
	}
}

func readBody(t *testing.T, sk Skill, c Consumer) string {
	t.Helper()
	b, err := sk.Body(c)
	if err != nil {
		t.Fatalf("%s.Body(%s): %v", sk.Name, c, err)
	}
	return string(b)
}

func skillNamed(t *testing.T, name string) Skill {
	t.Helper()
	for _, sk := range Skills() {
		if sk.Name == name {
			return sk
		}
	}
	t.Fatalf("no design skill named %q", name)
	return Skill{}
}

func headingTexts(body string) []string {
	var out []string
	for _, line := range proseLines(body) {
		if strings.HasPrefix(line, "#") {
			out = append(out, strings.TrimSpace(strings.TrimLeft(line, "#")))
		}
	}
	return out
}

func routerSectionCell(line string) (string, bool) {
	if !strings.HasPrefix(line, "|") {
		return "", false
	}
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	if len(cells) != 2 {
		return "", false
	}
	cell := strings.TrimSpace(cells[1])
	if cell == "Section" || strings.Trim(cell, "-: ") == "" {
		return "", false
	}
	return cell, true
}

func splitSectionNames(cell string) []string {
	var out []string
	for _, part := range strings.Split(cell, " — ") {
		for _, name := range strings.Split(part, ", ") {
			out = append(out, strings.TrimSpace(name))
		}
	}
	return out
}
