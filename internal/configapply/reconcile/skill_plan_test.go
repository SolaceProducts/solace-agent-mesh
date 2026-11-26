// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func writeSkillSourceDir(t *testing.T, name, body string) string {
	t.Helper()
	d := t.TempDir()
	md := "---\nname: " + name + "\ndescription: a skill\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return d
}

func planOneSkill(t *testing.T, dir, name, desc string, actual []wire.SkillResponse, opts BuildOptions) *skillPlan {
	t.Helper()
	desired := []resolve.ResolvedSkill{{
		Name:        name,
		Description: desc,
		SourceFile:  filepath.Join(dir, "SKILL.md"),
		BundleDir:   dir,
	}}
	plan, err := planSkills(context.Background(), desired, actual, t.TempDir(), opts)
	if err != nil {
		t.Fatalf("planSkills: %v", err)
	}
	return plan
}

func bundleHashFor(t *testing.T, dir, name, desc string) string {
	t.Helper()
	plan := planOneSkill(t, dir, name, desc, nil, BuildOptions{NoBuild: true})
	if len(plan.Creates) != 1 {
		t.Fatalf("expected 1 create to capture hash, got %d", len(plan.Creates))
	}
	return plan.Creates[0].Desired.bundleHash
}

func TestPlanSkills_MatchingHashIsUnchanged(t *testing.T) {
	dir := writeSkillSourceDir(t, "demo", "hello")
	hash := bundleHashFor(t, dir, "demo", "a skill")

	actual := []wire.SkillResponse{{
		ID:              "id-1",
		Name:            "demo",
		Description:     new("a skill"),
		DiscoveryStatus: "ready",
		Bundle:          &apiv2.SkillBundle{ContentHash: new(hash)},
	}}
	plan := planOneSkill(t, dir, "demo", "a skill", actual, BuildOptions{NoBuild: true})

	if len(plan.Unchanged) != 1 {
		t.Fatalf("expected 1 unchanged, got unchanged=%d updates=%d creates=%d",
			len(plan.Unchanged), len(plan.Updates), len(plan.Creates))
	}
	if plan.Unchanged[0].UploadNeeded {
		t.Errorf("Unchanged op must not set UploadNeeded")
	}
}

func TestPlanSkills_ChangedBundleReuploads(t *testing.T) {
	dir := writeSkillSourceDir(t, "demo", "new content that changes the hash")

	actual := []wire.SkillResponse{{
		ID:              "id-1",
		Name:            "demo",
		Description:     new("a skill"),
		DiscoveryStatus: "ready",
		Bundle:          &apiv2.SkillBundle{ContentHash: new("0000000000000000000000000000000000000000000000000000000000000000")},
	}}
	plan := planOneSkill(t, dir, "demo", "a skill", actual, BuildOptions{NoBuild: true})

	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update, got updates=%d unchanged=%d creates=%d",
			len(plan.Updates), len(plan.Unchanged), len(plan.Creates))
	}
	if !plan.Updates[0].UploadNeeded {
		t.Errorf("changed bundle must set UploadNeeded=true so apply re-uploads")
	}
}

func TestPlanSkills_DescriptionOnlyEditSkipsUpload(t *testing.T) {
	dir := writeSkillSourceDir(t, "demo", "hello")
	hash := bundleHashFor(t, dir, "demo", "new description")

	actual := []wire.SkillResponse{{
		ID:              "id-1",
		Name:            "demo",
		Description:     new("old description"),
		DiscoveryStatus: "ready",
		Bundle:          &apiv2.SkillBundle{ContentHash: new(hash)},
	}}
	plan := planOneSkill(t, dir, "demo", "new description", actual, BuildOptions{NoBuild: true})

	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update, got updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
	if plan.Updates[0].UploadNeeded {
		t.Errorf("description-only edit must skip the bundle re-upload (UploadNeeded=false)")
	}
}

func TestPlanSkills_FailedDiscoveryForcesReupload(t *testing.T) {
	dir := writeSkillSourceDir(t, "demo", "hello")
	hash := bundleHashFor(t, dir, "demo", "a skill")

	actual := []wire.SkillResponse{{
		ID:              "id-1",
		Name:            "demo",
		Description:     new("a skill"),
		DiscoveryStatus: "failed",
		Bundle:          &apiv2.SkillBundle{ContentHash: new(hash)},
	}}
	plan := planOneSkill(t, dir, "demo", "a skill", actual, BuildOptions{NoBuild: true})

	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update, got updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
	if !plan.Updates[0].UploadNeeded {
		t.Errorf("failed-discovery skill must re-upload to retrigger STR scan")
	}
}

func TestPlanSkills_MissingHashReuploads(t *testing.T) {
	dir := writeSkillSourceDir(t, "demo", "hello")

	actual := []wire.SkillResponse{{
		ID:              "id-1",
		Name:            "demo",
		Description:     new("a skill"),
		DiscoveryStatus: "ready",
		Bundle:          &apiv2.SkillBundle{ContentHash: new("")},
	}}
	plan := planOneSkill(t, dir, "demo", "a skill", actual, BuildOptions{NoBuild: true})

	if len(plan.Updates) != 1 || !plan.Updates[0].UploadNeeded {
		t.Fatalf("empty stored hash must re-upload; got updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
}
