// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func makeStatusMessage(parts ...map[string]any) map[string]any {
	anyParts := make([]any, 0, len(parts))
	for _, p := range parts {
		anyParts = append(anyParts, map[string]any{"kind": "data", "data": p})
	}
	return map[string]any{
		"result": map[string]any{
			"status": map[string]any{
				"message": map[string]any{
					"parts": anyParts,
				},
			},
		},
	}
}

func TestExtractArtifactsMetadataPicksSavedAndCompletedEvents(t *testing.T) {
	t.Parallel()
	messages := []map[string]any{
		{"payload": makeStatusMessage(map[string]any{
			"type":              "artifact_creation_progress",
			"filename":          "report.md",
			"status":            "in-progress",
			"bytes_transferred": float64(1024),
		})},
		{"payload": makeStatusMessage(map[string]any{
			"type":              "artifact_creation_progress",
			"filename":          "report.md",
			"status":            "completed",
			"bytes_transferred": float64(4096),
			"mime_type":         "text/markdown",
			"version":           float64(1),
		})},
		{"payload": makeStatusMessage(map[string]any{
			"type":       "artifact_saved",
			"filename":   "chart.png",
			"version":    float64(2),
			"mime_type":  "image/png",
			"size_bytes": float64(54321),
		})},
		{"payload": makeStatusMessage(map[string]any{
			"type":     "artifact_creation_progress",
			"filename": "ghost.txt",
			"status":   "in-progress",
		})},
	}

	got := extractArtifactsMetadata(messages)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2; got=%+v", len(got), got)
	}
	if got[0].Filename != "report.md" || got[0].SizeBytes != 4096 || got[0].MIMEType != "text/markdown" {
		t.Errorf("first metadata wrong: %+v", got[0])
	}
	if got[1].Filename != "chart.png" || got[1].Version != 2 || got[1].SizeBytes != 54321 || got[1].MIMEType != "image/png" {
		t.Errorf("second metadata wrong: %+v", got[1])
	}
}

func TestExtractArtifactsMetadataNeverOverwritesSavedWithProgress(t *testing.T) {
	t.Parallel()
	messages := []map[string]any{
		{"payload": makeStatusMessage(map[string]any{
			"type":       "artifact_saved",
			"filename":   "report.md",
			"version":    float64(1),
			"mime_type":  "text/markdown",
			"size_bytes": float64(1000),
		})},
		{"payload": makeStatusMessage(map[string]any{
			"type":              "artifact_creation_progress",
			"filename":          "report.md",
			"version":           float64(1),
			"status":            "completed",
			"bytes_transferred": float64(9999),
			"mime_type":         "text/plain",
		})},
	}
	got := extractArtifactsMetadata(messages)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1; got=%+v", len(got), got)
	}
	if got[0].SizeBytes != 1000 {
		t.Errorf("size = %d, want 1000 (artifact_saved authoritative)", got[0].SizeBytes)
	}
	if got[0].MIMEType != "text/markdown" {
		t.Errorf("mime = %q, want text/markdown (artifact_saved authoritative)", got[0].MIMEType)
	}
}

func TestExtractArtifactsMetadataReturnsNilForEmpty(t *testing.T) {
	t.Parallel()
	if got := extractArtifactsMetadata(nil); got != nil {
		t.Fatalf("nil messages: got %v, want nil", got)
	}
	if got := extractArtifactsMetadata([]map[string]any{{"payload": map[string]any{}}}); got != nil {
		t.Fatalf("empty payload: got %v, want nil", got)
	}
}

func TestEncodeArtifactsMetadataRoundTrip(t *testing.T) {
	t.Parallel()
	metas := []snapshotArtifactMeta{
		{Filename: "a.md", Version: 1, MIMEType: "text/markdown", SizeBytes: 100},
		{Filename: "b.png", Version: 2, MIMEType: "image/png", SizeBytes: 200},
	}
	encoded := encodeArtifactsMetadata(metas)
	if encoded == "" {
		t.Fatal("encoded empty for non-empty metas")
	}
	var decoded []snapshotArtifactMeta
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded) != 2 || decoded[0].Filename != "a.md" || decoded[1].SizeBytes != 200 {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
}

func TestEncodeArtifactsMetadataEmpty(t *testing.T) {
	t.Parallel()
	if encodeArtifactsMetadata(nil) != "" {
		t.Error("nil should encode to empty string")
	}
	if encodeArtifactsMetadata([]snapshotArtifactMeta{}) != "" {
		t.Error("empty slice should encode to empty string")
	}
}

func TestSnapshotArtifactsForJudgeNilServiceFallsBackToPlaceholders(t *testing.T) {
	t.Parallel()
	metas := []snapshotArtifactMeta{
		{Filename: "x.md", Version: 1, MIMEType: "text/markdown"},
		{Filename: "y.png", Version: 1, MIMEType: "image/png"},
	}
	blocks, updated := snapshotArtifactsForJudge(snapshotArtifactsForJudgeArgs{
		Ctx:            t.Context(),
		Logger:         discardLogger(),
		Artifacts:      nil,
		RunID:          "run-1",
		ExampleID:      "ex-1",
		AgentSessionID: "task-1",
		TrialIndex:     0,
		Metas:          metas,
	})
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	for _, b := range blocks {
		ph, ok := b.(ArtifactPlaceholder)
		if !ok {
			t.Errorf("expected ArtifactPlaceholder, got %T", b)
			continue
		}
		if ph.Reason == "" {
			t.Error("placeholder Reason should be set when service is nil")
		}
	}
	if len(updated) != len(metas) {
		t.Errorf("metas should pass through unchanged on nil service")
	}
}

func TestSnapshotArtifactsForJudgeReadsUnderRunPrincipal(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mem := memory.New()
	scoped := &artifact.ScopedService{Inner: mem, Scope: "namespace", Namespace: "ns"}

	const principal = "system:qa-bot"
	textBytes := []byte("# QA notes\n")
	agentLoc := scoped.Resolve("", principal, "task-qa", "notes.md")
	if _, err := mem.Save(ctx, agentLoc, textBytes, "text/markdown"); err != nil {
		t.Fatalf("seed agent artifact under %s: %v", principal, err)
	}

	blocks, _ := snapshotArtifactsForJudge(snapshotArtifactsForJudgeArgs{
		Ctx:            ctx,
		Logger:         discardLogger(),
		Artifacts:      scoped,
		RunID:          "run-qa",
		ExampleID:      "ex-1",
		AgentSessionID: "task-qa",
		Principal:      principal,
		TrialIndex:     0,
		Metas:          []snapshotArtifactMeta{{Filename: "notes.md", Version: 0, MIMEType: "text/markdown"}},
	})

	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	text, ok := blocks[0].(ArtifactJudgeText)
	if !ok {
		t.Fatalf("blocks[0] = %T, want ArtifactJudgeText — a source read scoped to the default eval user would miss the artifact entirely", blocks[0])
	}
	if text.Text != string(textBytes) {
		t.Errorf("inlined text = %q, want %q", text.Text, textBytes)
	}

	snapSessionID := evalRunSnapshotSessionID("run-qa")
	snapshotted, _, err := mem.Load(ctx, scoped.Resolve("", EvalRunnerUserID, snapSessionID, "notes.md"), nil)
	if err != nil {
		t.Fatalf("load snapshot under %s: %v", EvalRunnerUserID, err)
	}
	if string(snapshotted) != string(textBytes) {
		t.Errorf("snapshot under %s = %q, want %q", EvalRunnerUserID, snapshotted, textBytes)
	}
	underPrincipal, _, err := mem.Load(ctx, scoped.Resolve("", principal, snapSessionID, "notes.md"), nil)
	if err != nil {
		t.Fatalf("probe snapshot scope under %s: %v", principal, err)
	}
	if len(underPrincipal) != 0 {
		t.Errorf("snapshot also landed under the run principal %s; destinations must stay on the eval-owned namespace", principal)
	}
}

func TestSnapshotArtifactsForJudgeCopiesAndInlinesText(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mem := memory.New()
	scoped := &artifact.ScopedService{Inner: mem, Scope: "namespace", Namespace: "ns"}

	agentLoc := scoped.Resolve("", rbac.EvalSystemUserSub, "task-abc", "notes.md")
	textBytes := []byte("# Notes\n\nThis is the agent's report.\n")
	if _, err := mem.Save(ctx, agentLoc, textBytes, "text/markdown"); err != nil {
		t.Fatalf("seed agent artifact: %v", err)
	}

	binLoc := scoped.Resolve("", rbac.EvalSystemUserSub, "task-abc", "chart.png")
	if _, err := mem.Save(ctx, binLoc, []byte{0x89, 0x50, 0x4e, 0x47}, "image/png"); err != nil {
		t.Fatalf("seed binary artifact: %v", err)
	}

	metas := []snapshotArtifactMeta{
		{Filename: "notes.md", Version: 0, MIMEType: "text/markdown"},
		{Filename: "chart.png", Version: 0, MIMEType: "image/png"},
	}

	blocks, updated := snapshotArtifactsForJudge(snapshotArtifactsForJudgeArgs{
		Ctx:            ctx,
		Logger:         discardLogger(),
		Artifacts:      scoped,
		RunID:          "run-1",
		ExampleID:      "ex-1",
		AgentSessionID: "task-abc",
		Principal:      rbac.EvalSystemUserSub,
		TrialIndex:     0,
		Metas:          metas,
	})

	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	if _, ok := blocks[0].(ArtifactJudgeText); !ok {
		t.Errorf("blocks[0] = %T, want ArtifactJudgeText (text MIME should inline)", blocks[0])
	}
	if _, ok := blocks[1].(ArtifactPlaceholder); !ok {
		t.Errorf("blocks[1] = %T, want ArtifactPlaceholder (binary stays a placeholder)", blocks[1])
	}

	snapSessionID := evalRunSnapshotSessionID("run-1")
	snapLoc := scoped.Resolve("", EvalRunnerUserID, snapSessionID, "notes.md")
	got, mime, err := mem.Load(ctx, snapLoc, nil)
	if err != nil {
		t.Fatalf("load snapshotted notes.md: %v", err)
	}
	if string(got) != string(textBytes) || mime != "text/markdown" {
		t.Errorf("snapshotted content = %q (%s), want %q (text/markdown)", got, mime, textBytes)
	}

	if len(updated) != 2 {
		t.Fatalf("updated metas = %d", len(updated))
	}
	if updated[0].MIMEType != "text/markdown" {
		t.Errorf("notes.md mime = %q, want text/markdown", updated[0].MIMEType)
	}
}

func TestSnapshotArtifactsForJudgeLoadFailureFallsBackToPlaceholder(t *testing.T) {
	t.Parallel()
	mem := memory.New()
	scoped := &artifact.ScopedService{Inner: mem, Scope: "namespace", Namespace: "ns"}

	metas := []snapshotArtifactMeta{{Filename: "ghost.md", Version: 0, MIMEType: "text/markdown"}}
	blocks, _ := snapshotArtifactsForJudge(snapshotArtifactsForJudgeArgs{
		Ctx:            t.Context(),
		Logger:         discardLogger(),
		Artifacts:      scoped,
		RunID:          "run-1",
		ExampleID:      "ex-1",
		AgentSessionID: "task-abc",
		Principal:      rbac.EvalSystemUserSub,
		TrialIndex:     0,
		Metas:          metas,
	})
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	ph, ok := blocks[0].(ArtifactPlaceholder)
	if !ok {
		t.Fatalf("blocks[0] = %T, want ArtifactPlaceholder on missing artifact", blocks[0])
	}
	if ph.Reason == "" {
		t.Error("placeholder Reason should explain the missing bytes")
	}
}

func TestExtractArtifactsMetadataCarriesOptionalFields(t *testing.T) {
	t.Parallel()
	desc := "post-mortem chart"
	fnCallID := "fn-7"
	messages := []map[string]any{
		{"payload": makeStatusMessage(map[string]any{
			"type":             "artifact_saved",
			"filename":         "chart.png",
			"version":          float64(3),
			"mime_type":        "image/png",
			"size_bytes":       float64(20480),
			"description":      desc,
			"tags":             []any{"chart", "summary", 99},
			"function_call_id": fnCallID,
		})},
	}
	got := extractArtifactsMetadata(messages)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	m := got[0]
	if m.Filename != "chart.png" || m.Version != 3 || m.MIMEType != "image/png" || m.SizeBytes != 20480 {
		t.Errorf("basic fields wrong: %+v", m)
	}
	if m.Description == nil || *m.Description != desc {
		t.Errorf("Description = %v, want %q", m.Description, desc)
	}
	if m.FunctionCallID == nil || *m.FunctionCallID != fnCallID {
		t.Errorf("FunctionCallID = %v, want %q", m.FunctionCallID, fnCallID)
	}
	if len(m.Tags) != 2 || m.Tags[0] != "chart" || m.Tags[1] != "summary" {
		t.Errorf("Tags = %v, want [chart summary] (non-string entries filtered)", m.Tags)
	}
}

func TestExtractArtifactsMetadataIgnoresMissingFilename(t *testing.T) {
	t.Parallel()
	messages := []map[string]any{
		{"payload": makeStatusMessage(map[string]any{
			"type":       "artifact_saved",
			"size_bytes": float64(100),
		})},
		{"payload": makeStatusMessage(map[string]any{
			"type":              "artifact_creation_progress",
			"status":            "completed",
			"bytes_transferred": float64(50),
		})},
	}
	if got := extractArtifactsMetadata(messages); got != nil {
		t.Fatalf("got = %+v, want nil (filename-less events must be ignored)", got)
	}
}

func TestExtractArtifactsMetadataAcceptsIntSizeBytes(t *testing.T) {
	t.Parallel()
	messages := []map[string]any{
		{"payload": makeStatusMessage(map[string]any{
			"type":       "artifact_saved",
			"filename":   "a.bin",
			"version":    1,
			"size_bytes": int64(7777),
		})},
	}
	got := extractArtifactsMetadata(messages)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Version != 1 || got[0].SizeBytes != 7777 {
		t.Errorf("fields = %+v, want Version=1 SizeBytes=7777", got[0])
	}
}

func TestSnapshotArtifactsForJudgeHonorsExplicitVersion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mem := memory.New()
	scoped := &artifact.ScopedService{Inner: mem, Scope: "namespace", Namespace: "ns"}

	agentLoc := scoped.Resolve("", rbac.EvalSystemUserSub, "task-v", "report.md")
	v0Bytes := []byte("# v0 report\n")
	if _, err := mem.Save(ctx, agentLoc, v0Bytes, "text/markdown"); err != nil {
		t.Fatalf("seed v0: %v", err)
	}
	v1Bytes := []byte("# v1 report — REVISED\n")
	if _, err := mem.Save(ctx, agentLoc, v1Bytes, "text/markdown"); err != nil {
		t.Fatalf("seed v1: %v", err)
	}

	metas := []snapshotArtifactMeta{
		{Filename: "report.md", Version: 1, MIMEType: "text/markdown"},
	}
	blocks, updated := snapshotArtifactsForJudge(snapshotArtifactsForJudgeArgs{
		Ctx:            ctx,
		Logger:         discardLogger(),
		Artifacts:      scoped,
		RunID:          "run-1",
		ExampleID:      "ex-1",
		AgentSessionID: "task-v",
		Principal:      rbac.EvalSystemUserSub,
		TrialIndex:     0,
		Metas:          metas,
	})
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	if len(updated) != 1 {
		t.Fatalf("updated = %d, want 1", len(updated))
	}
	if updated[0].AgentVersion != 1 {
		t.Errorf("AgentVersion = %d, want 1 (agent's wire-emitted version)", updated[0].AgentVersion)
	}
	snapSessionID := evalRunSnapshotSessionID("run-1")
	snapLoc := scoped.Resolve("", EvalRunnerUserID, snapSessionID, "report.md")
	got, _, err := mem.Load(ctx, snapLoc, nil)
	if err != nil {
		t.Fatalf("load snapshotted report.md: %v", err)
	}
	if string(got) != string(v1Bytes) {
		t.Errorf("snapshotted = %q, want %q (v1 — explicit version Load)", got, v1Bytes)
	}
}

func TestSnapshotArtifactsForJudgeHonorsVersionZero(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mem := memory.New()
	scoped := &artifact.ScopedService{Inner: mem, Scope: "namespace", Namespace: "ns"}

	agentLoc := scoped.Resolve("", rbac.EvalSystemUserSub, "task-v0", "report.md")
	v0Bytes := []byte("# v0 report\n")
	if _, err := mem.Save(ctx, agentLoc, v0Bytes, "text/markdown"); err != nil {
		t.Fatalf("seed v0: %v", err)
	}
	if _, err := mem.Save(ctx, agentLoc, []byte("# v1 report — REVISED\n"), "text/markdown"); err != nil {
		t.Fatalf("seed v1: %v", err)
	}

	metas := []snapshotArtifactMeta{
		{Filename: "report.md", Version: 0, MIMEType: "text/markdown"},
	}
	_, updated := snapshotArtifactsForJudge(snapshotArtifactsForJudgeArgs{
		Ctx:            ctx,
		Logger:         discardLogger(),
		Artifacts:      scoped,
		RunID:          "run-0",
		ExampleID:      "ex-0",
		AgentSessionID: "task-v0",
		Principal:      rbac.EvalSystemUserSub,
		TrialIndex:     0,
		Metas:          metas,
	})
	if len(updated) != 1 {
		t.Fatalf("updated = %d, want 1", len(updated))
	}
	if updated[0].AgentVersion != 0 {
		t.Errorf("AgentVersion = %d, want 0 (agent's wire-emitted version)", updated[0].AgentVersion)
	}
	snapSessionID := evalRunSnapshotSessionID("run-0")
	snapLoc := scoped.Resolve("", EvalRunnerUserID, snapSessionID, "report.md")
	got, _, err := mem.Load(ctx, snapLoc, nil)
	if err != nil {
		t.Fatalf("load snapshotted report.md: %v", err)
	}
	if string(got) != string(v0Bytes) {
		t.Errorf("snapshotted = %q, want %q (v0 — explicit version 0 Load, not latest)", got, v0Bytes)
	}
}

type racingSnapshots struct {
	artifact.Service
	raced bool
}

func (r *racingSnapshots) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	versions, err := r.Service.ListVersions(ctx, loc)
	if !r.raced {
		r.raced = true
		if _, serr := r.Save(ctx, loc, []byte("other GWE"), "text/plain"); serr != nil {
			return nil, serr
		}
	}
	return versions, err
}

func TestSaveSnapshotVersion_ASaveThatLosesItsVersionTakesTheNext(t *testing.T) {
	store := &racingSnapshots{Service: memory.New()}
	loc := artifact.Locator{AppName: "ns", UserID: EvalRunnerUserID, SessionID: evalRunSnapshotSessionID("run-1"), Filename: "report.txt"}
	version, err := saveSnapshotVersion(t.Context(), store, loc, []byte("this GWE"), "text/plain")
	if err != nil || version != 1 {
		t.Fatalf("save = %d, %v; want version 1", version, err)
	}
	for v, want := range map[int]string{0: "other GWE", 1: "this GWE"} {
		got, _, err := store.Load(t.Context(), loc, &v)
		if err != nil || string(got) != want {
			t.Errorf("version %d = %q, %v; want %q", v, got, err, want)
		}
	}
}

type alwaysConflicting struct{ artifact.Service }

func (alwaysConflicting) SaveVersion(context.Context, artifact.Locator, int, []byte, string) error {
	return artifact.ErrVersionConflict
}

type unlistable struct{ artifact.Service }

func (unlistable) ListVersions(context.Context, artifact.Locator) ([]int, error) {
	return nil, errors.New("store down")
}

func TestSaveSnapshotVersion_GivesUp(t *testing.T) {
	loc := artifact.Locator{AppName: "ns", UserID: EvalRunnerUserID, SessionID: evalRunSnapshotSessionID("run-1"), Filename: "report.txt"}
	if _, err := saveSnapshotVersion(t.Context(), alwaysConflicting{memory.New()}, loc, []byte("x"), "text/plain"); !errors.Is(err, artifact.ErrVersionConflict) {
		t.Errorf("save against endless conflicts = %v, want the conflict after the last attempt", err)
	}
	if _, err := saveSnapshotVersion(t.Context(), unlistable{memory.New()}, loc, []byte("x"), "text/plain"); err == nil {
		t.Error("save with an unlistable store succeeded")
	}
}
