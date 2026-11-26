// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/build"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	skillbuild "github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func writeSkillFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestPlanSkills_NoToolsLeavesBuildColumnEmpty(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "skills", "plain")
	writeSkillFile(t, bundleDir, "SKILL.md", "---\nname: plain\n---\n# plain\n")
	resolved := resolve.ResolvedSkill{
		Name:            "plain",
		Description:     "x",
		SourceFile:      filepath.Join(bundleDir, "SKILL.md"),
		BundleDir:       bundleDir,
		ResolvedSkillMD: []byte("---\nname: plain\n---\n# plain\n"),
	}
	plan, err := planSkills(context.Background(), []resolve.ResolvedSkill{resolved}, nil, dir, BuildOptions{})
	if err != nil {
		t.Fatalf("planSkills: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected 1 create, got %d", len(plan.Creates))
	}
	if plan.Creates[0].BuildAction != "" || plan.Creates[0].BuildToolDirs != 0 {
		t.Errorf("expected empty BUILD column for skill without tools, got action=%q dirs=%d",
			plan.Creates[0].BuildAction, plan.Creates[0].BuildToolDirs)
	}
}

func TestPlanSkills_NoBuildPlanSkipsBundleStays(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "skills", "noplan")
	writeSkillFile(t, bundleDir, "SKILL.md", "---\nname: noplan\n---\n# noplan\n")
	writeSkillFile(t, bundleDir, "tools/manifest.yaml", `version: 1
tools:
  hello:
    runtime: go
    executable: bin
    tool_dir: bin
`)
	writeSkillFile(t, bundleDir, "tools/bin/go.mod", "module bin\n\ngo 1.22\n")
	writeSkillFile(t, bundleDir, "tools/bin/main.go", "package main\nfunc main(){}\n")

	resolved := resolve.ResolvedSkill{
		Name:            "noplan",
		SourceFile:      filepath.Join(bundleDir, "SKILL.md"),
		BundleDir:       bundleDir,
		ResolvedSkillMD: []byte("---\nname: noplan\n---\n# noplan\n"),
	}
	plan, err := planSkills(context.Background(), []resolve.ResolvedSkill{resolved}, nil, dir, BuildOptions{NoBuild: true})
	if err != nil {
		t.Fatalf("planSkills: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected 1 create, got %d", len(plan.Creates))
	}
	op := plan.Creates[0]
	if op.BuildAction != skillbuild.ToolBuildActionSkipped {
		t.Errorf("expected skipped action, got %q", op.BuildAction)
	}
	col := skillBuildColumn(op, FormatOptions{})
	if !strings.Contains(col, "skipped") {
		t.Errorf("BUILD column should report skipped: %q", col)
	}
}

func TestPlanSkills_DuplicateNamesRejected(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "skills", "dup")
	writeSkillFile(t, bundleDir, "SKILL.md", "---\nname: dup\n---\n# dup\n")
	r := resolve.ResolvedSkill{
		Name:            "dup",
		BundleDir:       bundleDir,
		SourceFile:      filepath.Join(bundleDir, "SKILL.md"),
		ResolvedSkillMD: []byte("---\nname: dup\n---\n# dup\n"),
	}
	_, err := planSkills(context.Background(), []resolve.ResolvedSkill{r, r}, nil, dir, BuildOptions{})
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestRenderSkillPlan_AllOpKinds(t *testing.T) {
	target := build.Target{OS: "linux", Arch: "arm64"}
	plan := &skillPlan{
		Kind: "skill",
		Creates: []skillOp{{
			Name: "fresh-build", Source: "skills/fresh-build/SKILL.md",
			Verb: "create", UploadNeeded: true,
			BuildAction: skillbuild.ToolBuildActionBuilt, BuildDuration: 1500 * time.Millisecond,
			BuildTarget: target, BuildToolDirs: 1,
		}},
		Updates: []skillOp{
			{Name: "cached", Verb: "update", UploadNeeded: true,
				BuildAction: skillbuild.ToolBuildActionCacheHit, BuildTarget: target, BuildToolDirs: 1},
			{Name: "metadata-only", Verb: "update", UploadNeeded: false},
		},
		Deletes:   []skillOp{{Name: "gone", Verb: "delete"}},
		Unchanged: []skillOp{{Name: "stale", Verb: "noop", BuildAction: skillbuild.ToolBuildActionSkipped, BuildToolDirs: 1, BuildTarget: target}},
	}
	var buf bytes.Buffer
	renderSkillPlan(&buf, plan, FormatOptions{Verbose: false})
	out := buf.String()
	for _, want := range []string{
		"fresh-build", "create", "BUILD: built linux/arm64 (1.5s)",
		"cached", "update (re-upload)", "BUILD: cache-hit linux/arm64",
		"metadata-only", "update",
		"gone", "delete",
		"stale", "no change", "BUILD: skipped",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderSkillPlan output missing %q\nfull output:\n%s", want, out)
		}
	}

	var emptyBuf bytes.Buffer
	renderSkillPlan(&emptyBuf, &skillPlan{Kind: "skill"}, FormatOptions{})
	if !strings.Contains(emptyBuf.String(), "skills") {
		t.Errorf("renderSkillPlan empty case missing skills label: %q", emptyBuf.String())
	}
}

func TestSkillBuildColumn_UnknownActionFallsThrough(t *testing.T) {
	op := skillOp{
		BuildAction:   skillbuild.ToolBuildAction("future-action"),
		BuildToolDirs: 1,
	}
	col := skillBuildColumn(op, FormatOptions{})
	if !strings.Contains(col, "future-action") {
		t.Errorf("default branch should render the unknown action verbatim: %q", col)
	}
}

func TestExecuteSkillFailsWithoutDesired(t *testing.T) {
	if _, err := executeCreateSkill(context.Background(), nil, skillOp{Name: "x"}); err == nil {
		t.Error("executeCreateSkill should fail when Desired is nil")
	}
	if err := executeUpdateSkill(context.Background(), nil, skillOp{Name: "x"}); err == nil {
		t.Error("executeUpdateSkill should fail when Desired is nil")
	}
}

func TestAggregateSkillBuild_PrefersBuiltOverCacheHit(t *testing.T) {
	outcomes := []skillbuild.ToolBuildOutcome{
		{ToolDir: "a", Action: skillbuild.ToolBuildActionCacheHit, Target: build.Target{OS: "linux", Arch: "arm64"}},
		{ToolDir: "b", Action: skillbuild.ToolBuildActionBuilt, Target: build.Target{OS: "linux", Arch: "arm64"}},
	}
	action, _, target, count := aggregateSkillBuild(outcomes)
	if action != skillbuild.ToolBuildActionBuilt {
		t.Errorf("aggregate action = %q, want %q", action, skillbuild.ToolBuildActionBuilt)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if target.OS != "linux" || target.Arch != "arm64" {
		t.Errorf("target = %+v", target)
	}
}

func TestPlanSkills_DownloadHashAlignsPlatform(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "skills", "x")
	writeSkillFile(t, bundleDir, "SKILL.md", "---\nname: x\n---\n# x\n")
	r := resolve.ResolvedSkill{
		Name:            "x",
		BundleDir:       bundleDir,
		SourceFile:      filepath.Join(bundleDir, "SKILL.md"),
		ResolvedSkillMD: []byte("---\nname: x\n---\n# x\n"),
	}
	existing := []wire.SkillResponse{{Name: "x", ID: "id-1"}}
	plan, err := planSkills(context.Background(), []resolve.ResolvedSkill{r}, existing, dir, BuildOptions{})
	if err != nil {
		t.Fatalf("planSkills: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update op, got %d", len(plan.Updates))
	}
	if plan.Updates[0].ID != "id-1" {
		t.Errorf("update op ID = %q", plan.Updates[0].ID)
	}
}
