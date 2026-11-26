// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmemory "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestEvalFilesOutbox_ThePollerDeletesADeletesFilesAtItsNextTick(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	datasets, examples, rows := store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalExampleArtifactRepo(db)
	scoped := &artifact.ScopedService{Inner: artmemory.New(), Scope: "namespace", Namespace: "ns"}
	files := eval.NewFileDeletes(nil, scoped, rows)

	poller := platpkg.NewOutboxPoller(store.NewOutboxRepo(db), platpkg.NewCompositeOutboxPublisher(evalFilesOutboxRoutes(files), logger), platpkg.DefaultOutboxPollerConfig(), logger)
	if err := poller.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = poller.Stop(ctx) })

	ds := &store.EvalDataset{Name: "outbox", CreatedBy: "u", UpdatedBy: "u"}
	if err := datasets.Create(ctx, ds); err != nil {
		t.Fatal(err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}
	if err := examples.Create(ctx, ex); err != nil {
		t.Fatal(err)
	}
	uploads := eval.NewExampleArtifactService(eval.ExampleArtifactServiceParams{
		Examples: examples, ArtifactRows: rows, Artifacts: scoped, FileDeletes: files, Config: eval.DefaultExampleArtifactConfig(),
	})
	if _, err := uploads.Set(ctx, ex.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("bytes"), ""); err != nil {
		t.Fatal(err)
	}
	row, err := examples.GetByID(ctx, ex.ID)
	if err != nil {
		t.Fatal(err)
	}
	session := row.ArtifactByRole(store.EvalArtifactRoleInput).SessionID
	stored := func() []string {
		keys, err := scoped.Inner.ListKeys(ctx, artifact.KeyScope{AppName: "ns", UserID: eval.EvalRunnerUserID, SessionID: session})
		if err != nil {
			t.Fatal(err)
		}
		return keys
	}
	if len(stored()) == 0 {
		t.Fatal("the upload stored no file")
	}

	if err := eval.NewExampleService(examples, datasets, logger).WithFileDeletes(files).Delete(ctx, ex.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(stored()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("stored keys = %v long after the delete, want the poller to have deleted them", stored())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
