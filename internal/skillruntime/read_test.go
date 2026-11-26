// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadResource_Happy(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")
	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}

	res, err := ReadResource(ctx, "references/auth.md", 0, 0)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if res.Path != "references/auth.md" {
		t.Errorf("Path: %q", res.Path)
	}
	if !strings.Contains(res.Content, "JWT bearer tokens") {
		t.Errorf("Content missing expected text: %q", res.Content)
	}
	if res.Truncated {
		t.Errorf("should not be truncated")
	}
}

func TestReadResource_LineRange(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")
	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}

	res, err := ReadResource(ctx, "references/auth.md", 2, 3)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if res.StartLine != 2 || res.EndLine != 3 {
		t.Errorf("range: got [%d,%d], want [2,3]", res.StartLine, res.EndLine)
	}
	if !strings.HasPrefix(res.Content, "JWT bearer tokens") {
		t.Errorf("content should start with line 2: %q", res.Content)
	}
	if !strings.Contains(res.Content, "Algorithm") {
		t.Errorf("content should contain line 3: %q", res.Content)
	}
	if strings.Contains(res.Content, "Header:") {
		t.Errorf("content should NOT contain line 4: %q", res.Content)
	}
}

func TestReadResource_LineRangeOpenEnded(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")
	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}

	res, err := ReadResource(ctx, "references/auth.md", 3, 0)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if res.StartLine != 3 {
		t.Errorf("StartLine: %d", res.StartLine)
	}
	if !strings.Contains(res.Content, "Algorithm") || !strings.Contains(res.Content, "Header") {
		t.Errorf("content missing expected lines: %q", res.Content)
	}
}

func TestReadResource_LineCap(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "big")
	var sb strings.Builder
	for range 1500 {
		sb.WriteString("line\n")
	}
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "# big")
	writeFile(t, filepath.Join(skillDir, "references", "long.txt"), sb.String())

	ctx := SkillContext{Name: "big", LocalPath: skillDir}
	res, err := ReadResource(ctx, "references/long.txt", 1, 1000)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if res.EndLine != MaxReadLines {
		t.Errorf("EndLine should clamp to %d, got %d", MaxReadLines, res.EndLine)
	}
	if !res.Truncated {
		t.Errorf("Truncated should be true when line cap hits")
	}
}

func TestReadResource_ByteCap(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "huge")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "# huge")
	big := strings.Repeat("a", MaxReadBytes+1024)
	writeFile(t, filepath.Join(skillDir, "references", "huge.txt"), big)

	ctx := SkillContext{Name: "huge", LocalPath: skillDir}
	res, err := ReadResource(ctx, "references/huge.txt", 0, 0)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if !res.Truncated {
		t.Errorf("Truncated should be true when byte cap hits")
	}
	if len(res.Content) != MaxReadBytes {
		t.Errorf("Content len: got %d, want %d", len(res.Content), MaxReadBytes)
	}
}

func TestReadResource_PathValidation(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")
	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}

	cases := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"empty path", "", ErrInvalidParam},
		{"absolute path", "/etc/passwd", ErrInvalidPath},
		{"traversal", "../../etc/passwd", ErrInvalidPath},
		{"outside references", "SKILL.md", ErrPathNotInResources},
		{"outside both dirs", "tools/manifest.yaml", ErrPathNotInResources},
		{"missing file", "references/missing.md", ErrInvalidPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadResource(ctx, tc.path, 0, 0)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("path %q: got err %v, want %v", tc.path, err, tc.wantErr)
			}
		})
	}
}

func TestReadResource_DirectoryNotFile(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")
	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}

	_, err := ReadResource(ctx, "references/", 0, 0)
	if err == nil {
		t.Errorf("expected error reading a directory, got nil")
	}
}

func TestReadResource_AsMap(t *testing.T) {
	res := ReadResult{
		SkillName: "x", Path: "references/a.md", Content: "c", StartLine: 1, EndLine: 2,
	}
	m := res.AsMap()
	if m["start_line"] != 1 || m["end_line"] != 2 {
		t.Errorf("range fields missing in AsMap: %v", m)
	}

	res2 := ReadResult{SkillName: "x", Path: "p", Content: "c"}
	m2 := res2.AsMap()
	if _, has := m2["start_line"]; has {
		t.Errorf("start_line should be omitted when zero")
	}
}
