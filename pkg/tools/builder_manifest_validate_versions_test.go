// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"gopkg.in/yaml.v3"
)

const versionedManifest = `
name: my-project
components:
  - filename: agent.yaml
    type: agent
    name: AgentOne
    origin: session
    status: validated
  - filename: other.yaml
    type: agent
    name: AgentTwo
    status: validated
  - filename: ""
    type: agent
    name: Existing
    origin: platform
    status: deployed
`

const versionedAgentConfig = "apps:\n  - name: %s\n    app_config:\n      agent_name: %s\n      instruction: hi\n"

func seedVersionedBuild(t *testing.T) (*mockArtifactService, *tool.Context) {
	t.Helper()
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := t.Context()
	saveTestArtifact(ctx, svc, toolCtx, manifestFilename, versionedManifest, SAMBuildManifestMIMEType)
	saveTestArtifact(ctx, svc, toolCtx, "agent.yaml", strings.NewReplacer("%s", "AgentOne").Replace(versionedAgentConfig), "application/x-yaml")
	saveTestArtifact(ctx, svc, toolCtx, "agent.yaml", strings.NewReplacer("%s", "AgentOne").Replace(versionedAgentConfig), "application/x-yaml")
	saveTestArtifact(ctx, svc, toolCtx, "other.yaml", strings.NewReplacer("%s", "AgentTwo").Replace(versionedAgentConfig), "application/x-yaml")
	return svc, toolCtx
}

func storedVersions(t *testing.T, manifestYAML string) map[string]int {
	t.Helper()
	raw, present := manifestRoot(t, manifestYAML)[fullValidationVersionsKey]
	if !present {
		return nil
	}
	versions, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want a mapping", fullValidationVersionsKey, raw)
	}
	out := make(map[string]int, len(versions))
	for filename, version := range versions {
		out[filename], _ = version.(int)
	}
	return out
}

func TestValidateBuildManifest_StampRecordsComponentVersions(t *testing.T) {
	svc, toolCtx := seedVersionedBuild(t)

	result := validateFull(t, toolCtx, versionedManifest)
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q: %s", result.Status, result.Message)
	}

	got := storedVersions(t, storedManifest(t, toolCtx, svc))
	want := map[string]int{"agent.yaml": 1, "other.yaml": 0}
	if !maps.Equal(got, want) {
		t.Errorf("%s = %v, want %v: session and origin-less components are recorded, platform ones are not", fullValidationVersionsKey, got, want)
	}
}

func TestValidateBuildManifest_RestampingWithTheSameVersionsWritesNothing(t *testing.T) {
	svc, toolCtx := seedVersionedBuild(t)
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", manifestFilename)

	validateFull(t, toolCtx, versionedManifest)
	afterFirst, _ := svc.ListVersions(t.Context(), loc)
	validateFull(t, toolCtx, versionedManifest)
	afterSecond, _ := svc.ListVersions(t.Context(), loc)

	if len(afterFirst) != 2 || len(afterSecond) != len(afterFirst) {
		t.Errorf("manifest versions after the first and second validation = %d, %d; want 2 and 2", len(afterFirst), len(afterSecond))
	}
}

func TestValidateBuildManifest_AComponentEditedAfterValidationMovesTheRecordedVersion(t *testing.T) {
	svc, toolCtx := seedVersionedBuild(t)
	validateFull(t, toolCtx, versionedManifest)

	saveTestArtifact(t.Context(), svc, toolCtx, "agent.yaml", strings.NewReplacer("%s", "AgentOne").Replace(versionedAgentConfig), "application/x-yaml")
	validateFull(t, toolCtx, versionedManifest)

	got := storedVersions(t, storedManifest(t, toolCtx, svc))
	if got["agent.yaml"] != 2 {
		t.Errorf("%s = %v, want agent.yaml at 2 after the edit", fullValidationVersionsKey, got)
	}
}

type versionBumpingService struct {
	*mockArtifactService
	bumpedFilename string
	listCalls      int
}

func (s *versionBumpingService) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	versions, err := s.mockArtifactService.ListVersions(ctx, loc)
	if err != nil || loc.Filename != s.bumpedFilename {
		return versions, err
	}
	s.listCalls++
	if s.listCalls > 1 {
		versions = append(slices.Clone(versions), len(versions))
	}
	return versions, nil
}

func TestValidateBuildManifest_AComponentChangedDuringValidationIsNotStamped(t *testing.T) {
	mock, _ := seedVersionedBuild(t)
	svc := &versionBumpingService{mockArtifactService: mock, bumpedFilename: "agent.yaml"}
	toolCtx := newTestToolCtx(svc)

	result := validateFull(t, toolCtx, versionedManifest)

	if valid, _ := result.Data["valid"].(bool); !valid {
		t.Fatalf("the manifest itself is valid: %v", result.Data)
	}
	if got, _ := result.Data["full_validation_not_stamped"].(string); got != componentChangedWhileValidating {
		t.Errorf("full_validation_not_stamped = %q, want %q", got, componentChangedWhileValidating)
	}
	if !strings.Contains(result.Message, componentChangedWhileValidating) {
		t.Errorf("message = %q, want it to tell the agent the build is not stamped", result.Message)
	}
	stored := storedManifest(t, toolCtx, mock)
	if _, stamped := manifestRoot(t, stored)[fullValidationPassedKey]; stamped {
		t.Errorf("a component moved while it was validated, but the manifest was stamped:\n%s", stored)
	}
}

func TestValidateBuildManifest_ListingVersionsFailingIsAStampFailure(t *testing.T) {
	svc, toolCtx := seedVersionedBuild(t)
	svc.failListVersions = map[string]bool{"agent.yaml": true}

	result := validateFull(t, toolCtx, versionedManifest)

	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error: the build is not deployable without a stamp", result.Status)
	}
	if failed, _ := result.Data["stamp_failed"].(bool); !failed {
		t.Errorf("stamp_failed missing from %v", result.Data)
	}
}

func TestValidateBuildManifest_AnOriginlessComponentWithAMissingFileFailsAndIsNotStamped(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := t.Context()
	saveTestArtifact(ctx, svc, toolCtx, manifestFilename, versionedManifest, SAMBuildManifestMIMEType)
	saveTestArtifact(ctx, svc, toolCtx, "agent.yaml", strings.NewReplacer("%s", "AgentOne").Replace(versionedAgentConfig), "application/x-yaml")

	result := validateFull(t, toolCtx, versionedManifest)

	if valid, _ := result.Data["valid"].(bool); valid {
		t.Fatalf("other.yaml has no origin and was never written, but validation passed: %v", result.Data)
	}
	if !strings.Contains(result.Message, "other.yaml") {
		t.Errorf("message = %q, want it to name the missing other.yaml", result.Message)
	}
	stored := storedManifest(t, toolCtx, svc)
	if _, stamped := manifestRoot(t, stored)[fullValidationPassedKey]; stamped {
		t.Errorf("a component's file is missing, but the manifest was stamped:\n%s", stored)
	}
}

func TestVersionedComponentFilenames(t *testing.T) {
	components := []map[string]any{
		{"name": "session", "filename": "a.yaml", "origin": "session"},
		{"name": "originless", "filename": "b.yaml"},
		{"name": "platform", "filename": "c.yaml", "origin": "platform"},
		{"name": "blocked", "filename": "d.yaml", "origin": "session", "status": componentStatusBlocked},
		{"name": "no file", "origin": "session"},
		{"name": "blank file", "filename": "  ", "origin": "session"},
	}

	got := versionedComponentFilenames(components)

	if want := []string{"a.yaml", "b.yaml"}; !slices.Equal(got, want) {
		t.Errorf("versionedComponentFilenames = %v, want %v", got, want)
	}
}

func TestStripFullValidationStamp_RemovesTheVersionsWithIt(t *testing.T) {
	stamped := "name: p\nfull_validation_passed: true\nfull_validation_versions:\n  agent.yaml: 3\n"

	stripped, hadStamp := stripFullValidationStamp(stamped)

	if !hadStamp {
		t.Fatal("hadStamp = false")
	}
	var root map[string]any
	if err := yaml.Unmarshal([]byte(stripped), &root); err != nil {
		t.Fatal(err)
	}
	if _, present := root[fullValidationVersionsKey]; present {
		t.Errorf("stripped manifest still carries %s:\n%s", fullValidationVersionsKey, stripped)
	}
	if _, present := root[fullValidationPassedKey]; present {
		t.Errorf("stripped manifest still carries %s:\n%s", fullValidationPassedKey, stripped)
	}
}

const sharedFilenameManifest = `
name: my-project
components:
  - filename: agent.yaml
    type: agent
    name: AgentOne
    origin: session
    status: validated
  - filename: agent.yaml
    type: agent
    name: AgentTwo
    origin: session
    status: validated
`

func TestValidateBuildManifest_TwoComponentsSharingAFilenameAreStamped(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(t.Context(), svc, toolCtx, manifestFilename, sharedFilenameManifest, SAMBuildManifestMIMEType)
	saveTestArtifact(t.Context(), svc, toolCtx, "agent.yaml", strings.NewReplacer("%s", "AgentOne").Replace(versionedAgentConfig), "application/x-yaml")

	result := validateFull(t, toolCtx, sharedFilenameManifest)

	if valid, _ := result.Data["valid"].(bool); !valid {
		t.Fatalf("the manifest is valid: %v", result.Data)
	}
	if reason, notStamped := result.Data["full_validation_not_stamped"]; notStamped {
		t.Errorf("full_validation_not_stamped = %v, want the build stamped", reason)
	}
	if got, want := storedVersions(t, storedManifest(t, toolCtx, svc)), map[string]int{"agent.yaml": 0}; !maps.Equal(got, want) {
		t.Errorf("%s = %v, want %v", fullValidationVersionsKey, got, want)
	}
}
