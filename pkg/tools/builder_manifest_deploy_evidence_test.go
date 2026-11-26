// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func evidenceToolCtx(t *testing.T) *tool.Context {
	t.Helper()
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	return &tool.Context{ArtifactService: svc, AgentName: "a", UserID: "u", SessionID: "s"}
}

func saveManifestVersion(t *testing.T, toolCtx *tool.Context, body string) {
	t.Helper()
	if _, err := toolCtx.Facade().SaveArtifactWithMetadata(context.Background(), manifestFilename,
		[]byte(body), SAMBuildManifestMIMEType, artifact.MetadataOpts{Tags: []string{"__working"}}); err != nil {
		t.Fatalf("save manifest version: %v", err)
	}
}

const manifestWithEvidence = `name: webcam
components:
  - filename: wf.yaml
    type: workflow
    name: WebcamObjectDetection
    status: deployed
    origin: session
    id: 01a020e7-6397-78dd-bed8-e3c20314a9c2
    deployed_version: 0
`

const manifestWithoutEvidence = `name: webcam
components:
  - filename: wf.yaml
    type: workflow
    name: WebcamObjectDetection
    status: validated
    origin: session
`

func TestRestoreDeployEvidence_RecoversFieldsTheAgentDropped(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)

	repaired, restored, err := restoreDeployEvidence(context.Background(), manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if len(restored) != 1 || restored[0] != "WebcamObjectDetection" {
		t.Fatalf("restored = %v, want [WebcamObjectDetection]", restored)
	}
	if !strings.Contains(repaired, "id: 01a020e7-6397-78dd-bed8-e3c20314a9c2") {
		t.Errorf("id not restored:\n%s", repaired)
	}
	if !strings.Contains(repaired, "deployed_version: 0") {
		t.Errorf("deployed_version not restored:\n%s", repaired)
	}
	if !strings.Contains(repaired, "status: validated") {
		t.Errorf("repair clobbered the agent's status:\n%s", repaired)
	}
}

func TestRestoreDeployEvidence_RestoresVersionZero(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)

	repaired, _, err := restoreDeployEvidence(context.Background(), manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if !strings.Contains(repaired, "deployed_version: 0") {
		t.Fatalf("version 0 dropped as if it were absent:\n%s", repaired)
	}
}

func TestRestoreDeployEvidence_ScansPastVersionsThatAlreadyLostIt(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)
	for range 5 {
		saveManifestVersion(t, toolCtx, manifestWithoutEvidence)
	}

	repaired, restored, err := restoreDeployEvidence(context.Background(), manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if len(restored) != 1 {
		t.Fatalf("restored = %v, want one component", restored)
	}
	if !strings.Contains(repaired, "deployed_version: 0") {
		t.Fatalf("did not scan past the versions that had already lost it:\n%s", repaired)
	}
}

func TestRestoreDeployEvidence_DoesNotOverwriteWhatIsAlreadyThere(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)

	current := strings.Replace(manifestWithEvidence, "deployed_version: 0", "deployed_version: 4", 1)
	repaired, restored, err := restoreDeployEvidence(context.Background(), current, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if len(restored) != 0 {
		t.Fatalf("restored = %v, want nothing to repair", restored)
	}
	if !strings.Contains(repaired, "deployed_version: 4") || strings.Contains(repaired, "deployed_version: 0") {
		t.Fatalf("clobbered newer evidence:\n%s", repaired)
	}
}

func TestRestoreDeployEvidence_MatchesOnNameOnly(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)

	renamed := strings.Replace(manifestWithoutEvidence, "WebcamObjectDetection", "SomethingElse", 1)
	repaired, restored, err := restoreDeployEvidence(context.Background(), renamed, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if len(restored) != 0 {
		t.Fatalf("restored = %v for a renamed component", restored)
	}
	if strings.Contains(repaired, "01a020e7") {
		t.Fatalf("inherited another component's platform id:\n%s", repaired)
	}
}

func TestRestoreDeployEvidence_NoHistoryIsNotAnError(t *testing.T) {
	toolCtx := evidenceToolCtx(t)

	repaired, restored, err := restoreDeployEvidence(context.Background(), manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if len(restored) != 0 {
		t.Fatalf("restored = %v with no history", restored)
	}
	if repaired != manifestWithoutEvidence {
		t.Fatalf("manifest changed with nothing to restore:\n%s", repaired)
	}
}

func TestValidateBuildManifest_PersistsRestoredEvidence(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)

	tl := &ValidateBuildManifestTool{}
	res, err := tl.Execute(context.Background(), map[string]any{"manifest_yaml": manifestWithoutEvidence}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}

	stored, _, err := toolCtx.Facade().LoadArtifact(context.Background(), manifestFilename, nil)
	if err != nil {
		t.Fatalf("load stored manifest: %v", err)
	}
	if !strings.Contains(string(stored), "deployed_version: 0") {
		t.Errorf("stored manifest still lacks deployed_version:\n%s", stored)
	}
	if !strings.Contains(string(stored), "id: 01a020e7-6397-78dd-bed8-e3c20314a9c2") {
		t.Errorf("stored manifest still lacks id:\n%s", stored)
	}
	if !strings.Contains(string(stored), "status: validated") {
		t.Errorf("stored manifest lost the agent's own edit:\n%s", stored)
	}
}

func TestValidateBuildManifest_NoRepairWritesNoVersionSoTheFrontendDoesNotRefetch(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)

	before, err := toolCtx.Facade().ListArtifactVersions(context.Background(), manifestFilename)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}

	tl := &ValidateBuildManifestTool{}
	if _, execErr := tl.Execute(context.Background(), map[string]any{"manifest_yaml": manifestWithEvidence}, toolCtx); execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}

	after, err := toolCtx.Facade().ListArtifactVersions(context.Background(), manifestFilename)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("versions grew from %d to %d with nothing to repair", len(before), len(after))
	}
}

func TestRestoreDeployEvidence_PartialVersionDoesNotShadowACompleteOlderOne(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)
	saveManifestVersion(t, toolCtx, strings.Replace(manifestWithEvidence, "    deployed_version: 0\n", "", 1))

	repaired, restored, err := restoreDeployEvidence(context.Background(), manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if len(restored) != 1 {
		t.Fatalf("restored = %v, want one component", restored)
	}
	if !strings.Contains(repaired, "id: 01a020e7-6397-78dd-bed8-e3c20314a9c2") {
		t.Errorf("id not restored:\n%s", repaired)
	}
	if !strings.Contains(repaired, "deployed_version: 0") {
		t.Errorf("the id-only version shadowed the older complete record:\n%s", repaired)
	}
}

func TestRestoreDeployEvidence_NewestEvidenceWins(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, strings.Replace(manifestWithEvidence, "deployed_version: 0", "deployed_version: 2", 1))
	saveManifestVersion(t, toolCtx, strings.Replace(manifestWithEvidence, "deployed_version: 0", "deployed_version: 5", 1))

	repaired, _, err := restoreDeployEvidence(context.Background(), manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if !strings.Contains(repaired, "deployed_version: 5") {
		t.Fatalf("restored a superseded version instead of the newest:\n%s", repaired)
	}
}

func TestValidateBuildManifest_RepairRetiresAForgedFullValidationStamp(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)

	forged := manifestWithoutEvidence + "full_validation_passed: true\n"
	tl := &ValidateBuildManifestTool{}
	res, err := tl.Execute(context.Background(), map[string]any{"manifest_yaml": forged}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if retired, _ := res.Data["retired_full_validation"].(bool); !retired {
		t.Error("the agent was not told its stamp was retired")
	}

	stored, _, err := toolCtx.Facade().LoadArtifact(context.Background(), manifestFilename, nil)
	if err != nil {
		t.Fatalf("load stored manifest: %v", err)
	}
	if strings.Contains(string(stored), "full_validation_passed") {
		t.Errorf("a stamp the manifest was never entitled to reached the artifact:\n%s", stored)
	}
	if !strings.Contains(string(stored), "deployed_version: 0") {
		t.Errorf("retiring the stamp lost the repair:\n%s", stored)
	}
}

func TestValidateBuildManifest_StampSurvivesWhenNothingNeedsRepair(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	stamped := manifestWithEvidence + "full_validation_passed: true\n"
	saveManifestVersion(t, toolCtx, stamped)

	tl := &ValidateBuildManifestTool{}
	res, err := tl.Execute(context.Background(), map[string]any{"manifest_yaml": stamped}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, retired := res.Data["retired_full_validation"]; retired {
		t.Error("retired a stamp with nothing to repair")
	}

	stored, _, err := toolCtx.Facade().LoadArtifact(context.Background(), manifestFilename, nil)
	if err != nil {
		t.Fatalf("load stored manifest: %v", err)
	}
	if !strings.Contains(string(stored), "full_validation_passed") {
		t.Errorf("dropped a stamp that was still earned:\n%s", stored)
	}
}

func TestRestoreDeployEvidence_SkipsAmbiguousDuplicateNames(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)

	duplicated := `name: webcam
components:
  - filename: wf.yaml
    type: workflow
    name: WebcamObjectDetection
    status: validated
    origin: session
  - filename: wf2.yaml
    type: workflow
    name: WebcamObjectDetection
    status: validated
    origin: session
`
	repaired, restored, err := restoreDeployEvidence(context.Background(), duplicated, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if len(restored) != 0 {
		t.Fatalf("restored = %v, want nothing stamped onto an ambiguous name", restored)
	}
	if strings.Contains(repaired, "id: 01a020e7-6397-78dd-bed8-e3c20314a9c2") {
		t.Errorf("one component's platform id was smeared across its namesake:\n%s", repaired)
	}
}

func TestRestoreDeployEvidence_IgnoresAnIDThatIsNotAPlatformID(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, strings.Replace(manifestWithEvidence, "id: 01a020e7-6397-78dd-bed8-e3c20314a9c2", "id: the-workflow-i-just-made", 1))

	repaired, _, err := restoreDeployEvidence(context.Background(), manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if strings.Contains(repaired, "the-workflow-i-just-made") {
		t.Errorf("an agent-authored id was restored as deploy evidence:\n%s", repaired)
	}
	if !strings.Contains(repaired, "deployed_version: 0") {
		t.Errorf("a bad id took the good version down with it:\n%s", repaired)
	}
}

func TestRestoreDeployEvidence_WalksPastAnUnreadableVersion(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)
	saveManifestVersion(t, toolCtx, "\tthis: is: not: yaml\n")

	repaired, restored, err := restoreDeployEvidence(context.Background(), manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if len(restored) != 1 {
		t.Fatalf("restored = %v, want the component behind the corrupt version", restored)
	}
	if !strings.Contains(repaired, "deployed_version: 0") {
		t.Errorf("a corrupt intermediate version ended the walk:\n%s", repaired)
	}
}

func TestRestoreDeployEvidence_StopsOnCancelledContext(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithEvidence)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, restored, err := restoreDeployEvidence(ctx, manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if len(restored) != 0 {
		t.Fatalf("restored = %v after the context was cancelled", restored)
	}
}

func TestValidateBuildManifest_DoesNotClaimARepairItCouldNotPersist(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, manifestFilename, manifestWithEvidence, SAMBuildManifestMIMEType)
	svc.failSaves = map[string]int{manifestFilename: stampSaveMaxAttempts}

	tl := &ValidateBuildManifestTool{}
	res, err := tl.Execute(ctx, map[string]any{"manifest_yaml": manifestWithoutEvidence}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, claimed := res.Data["restored_deploy_evidence"]; claimed {
		t.Error("reported a restore that never reached the store")
	}
	if failed, _ := res.Data["restore_persist_failed"].(bool); !failed {
		t.Error("the agent was not told the repair did not land")
	}
	if valid, _ := res.Data["valid"].(bool); !valid {
		t.Error("a failed bookkeeping write failed the whole validation")
	}
}

const manifestStampedWithEvidence = `name: webcam
full_validation_passed: true
components:
  - filename: wf.yaml
    type: workflow
    name: WebcamObjectDetection
    status: deployed
    origin: session
    id: 01a020e7-6397-78dd-bed8-e3c20314a9c2
    deployed_version: 0
`

const manifestStampedWithoutEvidence = `name: webcam
full_validation_passed: true
components:
  - filename: wf.yaml
    type: workflow
    name: WebcamObjectDetection
    status: validated
    origin: session
`

func TestValidateBuildManifest_DoesNotClaimARetirementItCouldNotPersist(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, manifestFilename, manifestStampedWithEvidence, SAMBuildManifestMIMEType)
	svc.failSaves = map[string]int{manifestFilename: stampSaveMaxAttempts}

	tl := &ValidateBuildManifestTool{}
	res, err := tl.Execute(ctx, map[string]any{"manifest_yaml": manifestStampedWithoutEvidence}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, claimed := res.Data["retired_full_validation"]; claimed {
		t.Error("reported a stamp retirement that never reached the store")
	}
	if failed, _ := res.Data["restore_persist_failed"].(bool); !failed {
		t.Error("the agent was not told the write did not land")
	}
}

func TestValidateBuildManifest_ReportsTheRetirementItDidPersist(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, manifestFilename, manifestStampedWithEvidence, SAMBuildManifestMIMEType)

	tl := &ValidateBuildManifestTool{}
	res, err := tl.Execute(ctx, map[string]any{"manifest_yaml": manifestStampedWithoutEvidence}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if retired, _ := res.Data["retired_full_validation"].(bool); !retired {
		t.Error("a retirement that landed was not reported, so the agent cannot know to re-validate")
	}
}

const manifestWithQuotedVersion = `name: webcam
components:
  - filename: wf.yaml
    type: workflow
    name: WebcamObjectDetection
    status: deployed
    origin: session
    id: 01a020e7-6397-78dd-bed8-e3c20314a9c2
    deployed_version: "3"
`

func TestRestoreDeployEvidence_RefusesAVersionTheCanvasCannotRead(t *testing.T) {
	toolCtx := evidenceToolCtx(t)
	saveManifestVersion(t, toolCtx, manifestWithQuotedVersion)

	repaired, restored, err := restoreDeployEvidence(context.Background(), manifestWithoutEvidence, toolCtx)
	if err != nil {
		t.Fatalf("restoreDeployEvidence: %v", err)
	}
	if strings.Contains(repaired, `"3"`) {
		t.Errorf("restored a quoted deployed_version, which resolves the manifest to null on the canvas:\n%s", repaired)
	}
	if strings.Contains(repaired, "deployed_version") {
		t.Errorf("restored a deployed_version at all from a value it could not trust:\n%s", repaired)
	}
	if len(restored) != 1 || !strings.Contains(repaired, "01a020e7-6397-78dd-bed8-e3c20314a9c2") {
		t.Errorf("a bad version suppressed the good id too: restored=%v\n%s", restored, repaired)
	}
}

func TestValidateBuildManifest_TellsTheAgentWhichFieldTheCanvasWillReject(t *testing.T) {
	tl := &ValidateBuildManifestTool{}
	res, err := tl.Execute(context.Background(), map[string]any{"manifest_yaml": manifestWithQuotedVersion}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if valid, _ := res.Data["valid"].(bool); valid {
		t.Fatal("a deployed_version the canvas cannot parse passed validation, so the agent is never told which field broke the build")
	}
	if !strings.Contains(strings.ToLower(res.Message), "deployed_version") {
		t.Errorf("the failure does not name the offending field: %s", res.Message)
	}
}
