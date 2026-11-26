// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestArtifactResponse_MarshalsVersionZero(t *testing.T) {
	out, err := json.Marshal(ArtifactResponse{Filename: "out.txt", MimeType: "text/plain", Version: 0, Size: 7})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"version":0`) {
		t.Errorf("v0 dropped from wire payload: %s", out)
	}
}

func TestArtifactsFromMetadata(t *testing.T) {
	t.Run("nil and empty yield no artifacts", func(t *testing.T) {
		if got := artifactsFromMetadata(nil, "result-1", nil); got != nil {
			t.Errorf("nil = %v, want nil", got)
		}
		empty := ""
		if got := artifactsFromMetadata(&empty, "result-1", nil); got != nil {
			t.Errorf("empty = %v, want nil", got)
		}
	})

	t.Run("decodes snake_case metadata to camelCase DTO", func(t *testing.T) {
		meta := `[{"filename":"out.txt","version":2,"mime_type":"text/plain","size_bytes":12,"agent_version":1},
		          {"filename":"chart.png","version":1,"mime_type":"image/png","size_bytes":2048}]`
		got := artifactsFromMetadata(&meta, "result-1", nil)
		if len(got) != 2 {
			t.Fatalf("want 2 artifacts, got %d", len(got))
		}
		if got[0] != (ArtifactResponse{Filename: "out.txt", MimeType: "text/plain", Version: 2, Size: 12}) {
			t.Errorf("artifact[0] = %+v", got[0])
		}
		if got[1] != (ArtifactResponse{Filename: "chart.png", MimeType: "image/png", Version: 1, Size: 2048}) {
			t.Errorf("artifact[1] = %+v", got[1])
		}
	})

	t.Run("malformed json yields no artifacts", func(t *testing.T) {
		bad := "not json"
		if got := artifactsFromMetadata(&bad, "result-1", nil); got != nil {
			t.Errorf("malformed = %v, want nil", got)
		}
	})
}

func TestRunService_GetRunResultArtifact(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	dsRepo := store.NewEvalDatasetRepo(db)
	exRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resRepo := store.NewEvalRunResultRepo(db)

	ds := &store.EvalDataset{Name: "ds", CreatedBy: "u", UpdatedBy: "u"}
	if err := dsRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}
	if err := exRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &store.EvalExperiment{Name: "e", DatasetID: &ds.ID, TargetAgent: "a", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, Model: "m", StartedBy: "u"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	mem := memory.New()
	scoped := &artifact.ScopedService{Inner: mem, Scope: "namespace", Namespace: "ns"}
	sessionID := evalArtifactSnapshotSessionID(run.ID, ex.ID, 0)
	ref := scoped.Resolve("", EvalRunnerUserID, sessionID, "out.txt")
	version, err := scoped.Inner.Save(ctx, ref, []byte("hello world"), "text/plain")
	if err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	meta := fmt.Sprintf(`[{"filename":"out.txt","version":%d,"mime_type":"text/plain","size_bytes":11}]`, version)
	res := &store.EvalRunResult{RunID: run.ID, ExampleID: ex.ID, TrialIndex: 0, ArtifactsMetadata: &meta}
	if err := resRepo.Create(ctx, res); err != nil {
		t.Fatalf("create result: %v", err)
	}

	svc := NewRunService(RunServiceParams{Results: resRepo, Artifacts: scoped})

	t.Run("loads artifact bytes", func(t *testing.T) {
		content, mimeType, err := svc.GetRunResultArtifact(ctx, res.ID, "out.txt", nil)
		if err != nil {
			t.Fatalf("get artifact: %v", err)
		}
		if string(content) != "hello world" {
			t.Errorf("content = %q, want %q", content, "hello world")
		}
		if mimeType != "text/plain" {
			t.Errorf("mimeType = %q, want text/plain", mimeType)
		}
	})

	t.Run("recorded version 0 loads that version, not latest", func(t *testing.T) {
		dupSession := evalArtifactSnapshotSessionID(run.ID, ex.ID, 1)
		dupRef := scoped.Resolve("", EvalRunnerUserID, dupSession, "dup.txt")
		v0, err := scoped.Inner.Save(ctx, dupRef, []byte("v0-bytes"), "text/plain")
		if err != nil {
			t.Fatalf("save v0: %v", err)
		}
		if v0 != 0 {
			t.Fatalf("first save version = %d, want 0", v0)
		}
		if _, err = scoped.Inner.Save(ctx, dupRef, []byte("v1-bytes"), "text/plain"); err != nil {
			t.Fatalf("save v1: %v", err)
		}
		dupMeta := fmt.Sprintf(`[{"filename":"dup.txt","version":%d,"mime_type":"text/plain","size_bytes":8}]`, v0)
		dupRes := &store.EvalRunResult{RunID: run.ID, ExampleID: ex.ID, TrialIndex: 1, ArtifactsMetadata: &dupMeta}
		if err = resRepo.Create(ctx, dupRes); err != nil {
			t.Fatalf("create result: %v", err)
		}
		content, _, err := svc.GetRunResultArtifact(ctx, dupRes.ID, "dup.txt", nil)
		if err != nil {
			t.Fatalf("get artifact: %v", err)
		}
		if string(content) != "v0-bytes" {
			t.Errorf("content = %q, want %q (loaded latest instead of recorded v0?)", content, "v0-bytes")
		}
	})

	t.Run("version pin selects exact entry", func(t *testing.T) {
		pinSession := evalArtifactSnapshotSessionID(run.ID, ex.ID, 3)
		pinRef := scoped.Resolve("", EvalRunnerUserID, pinSession, "pinned.txt")
		v0, err := scoped.Inner.Save(ctx, pinRef, []byte("pin-v0"), "text/plain")
		if err != nil {
			t.Fatalf("save v0: %v", err)
		}
		v1, err := scoped.Inner.Save(ctx, pinRef, []byte("pin-v1"), "text/plain")
		if err != nil {
			t.Fatalf("save v1: %v", err)
		}
		pinMeta := fmt.Sprintf(
			`[{"filename":"pinned.txt","version":%d,"mime_type":"text/plain","size_bytes":6},`+
				`{"filename":"pinned.txt","version":%d,"mime_type":"text/plain","size_bytes":6}]`,
			v0, v1,
		)
		pinRes := &store.EvalRunResult{RunID: run.ID, ExampleID: ex.ID, TrialIndex: 3, ArtifactsMetadata: &pinMeta}
		if err = resRepo.Create(ctx, pinRes); err != nil {
			t.Fatalf("create result: %v", err)
		}

		gotV0, _, err := svc.GetRunResultArtifact(ctx, pinRes.ID, "pinned.txt", &v0)
		if err != nil {
			t.Fatalf("pinned v0: %v", err)
		}
		if string(gotV0) != "pin-v0" {
			t.Errorf("pinned v0 content = %q, want pin-v0", gotV0)
		}
		gotV1, _, err := svc.GetRunResultArtifact(ctx, pinRes.ID, "pinned.txt", &v1)
		if err != nil {
			t.Fatalf("pinned v1: %v", err)
		}
		if string(gotV1) != "pin-v1" {
			t.Errorf("pinned v1 content = %q, want pin-v1", gotV1)
		}

		gotDefault, _, err := svc.GetRunResultArtifact(ctx, pinRes.ID, "pinned.txt", nil)
		if err != nil {
			t.Fatalf("default: %v", err)
		}
		if string(gotDefault) != "pin-v1" {
			t.Errorf("default content = %q, want pin-v1 (highest recorded)", gotDefault)
		}

		missing := 999
		_, _, err = svc.GetRunResultArtifact(ctx, pinRes.ID, "pinned.txt", &missing)
		_ = asNotFoundError(t, err)
	})

	t.Run("storage load error is a server error, not 404", func(t *testing.T) {
		badSession := evalArtifactSnapshotSessionID(run.ID, ex.ID, 2)
		badRef := scoped.Resolve("", EvalRunnerUserID, badSession, "out.txt")
		if _, err := scoped.Inner.Save(ctx, badRef, []byte("present"), "text/plain"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		badMeta := `[{"filename":"out.txt","version":99,"mime_type":"text/plain","size_bytes":7}]`
		badRes := &store.EvalRunResult{RunID: run.ID, ExampleID: ex.ID, TrialIndex: 2, ArtifactsMetadata: &badMeta}
		if err := resRepo.Create(ctx, badRes); err != nil {
			t.Fatalf("create result: %v", err)
		}
		_, _, err := svc.GetRunResultArtifact(ctx, badRes.ID, "out.txt", nil)
		if err == nil {
			t.Fatal("expected a storage load error, got nil")
		}
		var nf *NotFoundError
		if errors.As(err, &nf) {
			t.Errorf("storage Load error masqueraded as NotFoundError: %v", err)
		}
	})

	t.Run("unknown filename is not found", func(t *testing.T) {
		_, _, err := svc.GetRunResultArtifact(ctx, res.ID, "missing.txt", nil)
		_ = asNotFoundError(t, err)
	})

	t.Run("nil artifact store is not found", func(t *testing.T) {
		nilSvc := NewRunService(RunServiceParams{Results: resRepo})
		_, _, err := nilSvc.GetRunResultArtifact(ctx, res.ID, "out.txt", nil)
		_ = asNotFoundError(t, err)
	})
}
