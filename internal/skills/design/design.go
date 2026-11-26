// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package design

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
)

//go:embed shared/*.md builder/*.md cli/*.md
var staticFS embed.FS

type Consumer string

const (
	Builder Consumer = "builder"
	CLI     Consumer = "cli"
)

func (c Consumer) SectionHeading() string {
	switch c {
	case Builder:
		return "Working in the Builder"
	case CLI:
		return "Working with declarative config"
	}
	return "Working in " + string(c)
}

type Skill struct {
	Name     string
	Guide    string
	BodyFile string
}

var skills = []Skill{
	{Name: "sam-best-practices", Guide: "best-practices", BodyFile: "sam-best-practices.md"},
	{Name: "sam-concepts", Guide: "concepts", BodyFile: "sam-concepts.md"},
	{Name: "sam-agent-design", Guide: "agent-design", BodyFile: "agent-design-guide.md"},
	{Name: "sam-workflow-design", Guide: "workflow-design", BodyFile: "workflow-design-guide.md"},
	{Name: "sam-skill-design", Guide: "skill-design", BodyFile: "skill-design-guide.md"},
	{Name: "sam-efficiency", Guide: "efficiency", BodyFile: "efficiency-guide.md"},
}

func Skills() []Skill {
	out := make([]Skill, len(skills))
	copy(out, skills)
	return out
}

func (s Skill) Router() ([]byte, error) {
	return staticFS.ReadFile(s.routerPath())
}

func (s Skill) Body(c Consumer) ([]byte, error) {
	shared, err := staticFS.ReadFile(s.sharedPath())
	if err != nil {
		return nil, fmt.Errorf("read shared design guide %s: %w", s.Guide, err)
	}
	section, err := staticFS.ReadFile(s.sectionPath(c))
	if errors.Is(err, fs.ErrNotExist) {
		return shared, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s section of design guide %s: %w", c, s.Guide, err)
	}
	var out bytes.Buffer
	out.Write(shared)
	if !bytes.HasSuffix(shared, []byte("\n")) {
		out.WriteByte('\n')
	}
	out.WriteString("\n## " + c.SectionHeading() + "\n\n")
	out.Write(section)
	return out.Bytes(), nil
}

func (s Skill) sharedPath() string            { return "shared/" + s.Guide + ".md" }
func (s Skill) routerPath() string            { return "builder/" + s.Guide + ".router.md" }
func (s Skill) sectionPath(c Consumer) string { return string(c) + "/" + s.Guide + ".md" }

func FS() fs.FS { return staticFS }
