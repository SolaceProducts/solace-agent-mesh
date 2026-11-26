// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing"
)

type beforeFirstSaveVersion struct {
	artifact.Service
	once sync.Once
	hook func()
}

func (s *beforeFirstSaveVersion) SaveVersion(ctx context.Context, loc artifact.Locator, version int, content []byte, mimeType string) error {
	s.once.Do(s.hook)
	return s.Service.SaveVersion(ctx, loc, version, content, mimeType)
}

type alwaysConflicting struct {
	artifact.Service
	attempts atomic.Int32
}

func (s *alwaysConflicting) SaveVersion(context.Context, artifact.Locator, int, []byte, string) error {
	s.attempts.Add(1)
	return artifact.ErrVersionConflict
}

type savedByTheWinner struct {
	artifact.Service
	attempts atomic.Int32
}

func (s *savedByTheWinner) SaveVersion(ctx context.Context, loc artifact.Locator, version int, content []byte, mimeType string) error {
	if s.attempts.Add(1) == maxIndexSaveAttempts {
		if err := s.Service.SaveVersion(ctx, loc, version, content, mimeType); err != nil {
			return err
		}
	}
	return artifact.ErrVersionConflict
}

func discardIndexer(artSvc *artifact.ScopedService) *projectIndexer {
	return &projectIndexer{artSvc: artSvc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func scopedStore(inner artifact.Service) *artifact.ScopedService {
	return &artifact.ScopedService{Inner: inner, Scope: "namespace", Namespace: "test-ns"}
}

func seedProjectFile(t *testing.T, artSvc *artifact.ScopedService, userID, projectID, filename, content string) {
	t.Helper()
	loc := artSvc.Resolve("", userID, projectSessionPrefix+projectID, filename)
	if _, err := artSvc.Inner.Save(t.Context(), loc, []byte(content), "text/plain"); err != nil {
		t.Fatalf("seed %s: %v", filename, err)
	}
}

func latestIndex(t *testing.T, artSvc *artifact.ScopedService, userID, projectID string) (int, indexing.Sources, []byte) {
	t.Helper()
	ctx := t.Context()
	loc := artSvc.Resolve("", userID, projectSessionPrefix+projectID, artifact.BM25IndexFilename)
	versions, err := artSvc.Inner.ListVersions(ctx, loc)
	if err != nil || len(versions) == 0 {
		t.Fatalf("index versions = %v, err = %v; want at least one", versions, err)
	}
	latest := versions[len(versions)-1]
	data, _, err := artSvc.Inner.Load(ctx, loc, &latest)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	sources, found, err := indexing.ReadSources(data)
	if err != nil || !found {
		t.Fatalf("read sources: found=%v err=%v", found, err)
	}
	return latest, sources, data
}

func currentSources(t *testing.T, artSvc *artifact.ScopedService, userID, projectID string) []indexing.SourceFile {
	t.Helper()
	files, err := projectSourceFiles(t.Context(), artSvc, projectID, userID)
	if err != nil {
		t.Fatalf("list project files: %v", err)
	}
	return files
}

func indexChunkTexts(t *testing.T, data []byte) string {
	t.Helper()
	built, err := indexing.DeserializeFromZIP(data)
	if err != nil {
		t.Fatalf("deserialize index: %v", err)
	}
	var texts []string
	for _, c := range built.Manifest.Chunks {
		texts = append(texts, c.ChunkText)
	}
	return strings.Join(texts, "\n")
}

func TestRebuildIndex_ConcurrentRebuildsEndWithNewerFiles(t *testing.T) {
	shared := memory.New()
	const userID, projectID = "alice", "proj-race"
	gweB := discardIndexer(scopedStore(shared))
	seedProjectFile(t, gweB.artSvc, userID, projectID, "notes.txt", "old planet pluto")

	gweAStore := &beforeFirstSaveVersion{Service: shared}
	gweA := discardIndexer(scopedStore(gweAStore))
	gweAStore.hook = func() {
		seedProjectFile(t, gweB.artSvc, userID, projectID, "notes.txt", "new planet ceres")
		gweB.rebuildIndex(t.Context(), projectID, userID, "", "")
	}

	gweA.rebuildIndex(t.Context(), projectID, userID, "", "")

	_, sources, data := latestIndex(t, gweB.artSvc, userID, projectID)
	if want := currentSources(t, gweB.artSvc, userID, projectID); !indexing.SameSourceFiles(sources.Files, want) {
		t.Fatalf("index sources = %+v, want the current files %+v", sources.Files, want)
	}
	if text := indexChunkTexts(t, data); !strings.Contains(text, "ceres") || strings.Contains(text, "pluto") {
		t.Fatalf("index holds %q, want the newer file only", text)
	}
}

func TestRebuildIndex_ConflictRetriesOnCurrentFiles(t *testing.T) {
	shared := memory.New()
	const userID, projectID = "alice", "proj-retry"
	artSvc := scopedStore(shared)
	seedProjectFile(t, artSvc, userID, projectID, "a.txt", "alpha content")

	indexLoc := artSvc.Resolve("", userID, projectSessionPrefix+projectID, artifact.BM25IndexFilename)
	racing := &beforeFirstSaveVersion{Service: shared}
	racing.hook = func() {
		staleZip, err := indexing.BuildProjectIndex(t.Context(), projectID, userID, artSvc, nil, nil, slog.New(slog.DiscardHandler), nil)
		if err != nil {
			t.Fatalf("build stale index: %v", err)
		}
		if err := shared.SaveVersion(t.Context(), indexLoc, 0, staleZip, "application/zip"); err != nil {
			t.Fatalf("save racing index: %v", err)
		}
		seedProjectFile(t, artSvc, userID, projectID, "b.txt", "beta content")
	}
	ix, mgr := newTestIndexer(scopedStore(racing))
	const taskID = "55555555-5555-5555-5555-555555555555"
	mgr.RegisterTask(taskID, projectSessionPrefix+projectID, userID)

	ix.rebuildIndex(t.Context(), projectID, userID, "", taskID)

	latest, sources, _ := latestIndex(t, artSvc, userID, projectID)
	if latest != 1 {
		t.Fatalf("latest index version = %d, want 1 (saved after the racing version 0)", latest)
	}
	if want := currentSources(t, artSvc, userID, projectID); !indexing.SameSourceFiles(sources.Files, want) || sources.IndexedFiles != 2 {
		t.Fatalf("index sources = %+v, want both current files indexed", sources)
	}
	if got := drainIndexEvents(t, ix, taskID); !slices.Equal(got, []string{"task_completed"}) {
		t.Fatalf("events = %v, want [task_completed]", got)
	}
}

func TestRebuildIndex_GivesUpAfterRepeatedConflicts(t *testing.T) {
	shared := memory.New()
	const userID, projectID = "alice", "proj-giveup"
	conflicting := &alwaysConflicting{Service: shared}
	seedProjectFile(t, scopedStore(shared), userID, projectID, "a.txt", "alpha content")
	ix, mgr := newTestIndexer(scopedStore(conflicting))
	const taskID = "66666666-6666-6666-6666-666666666666"
	mgr.RegisterTask(taskID, projectSessionPrefix+projectID, userID)

	ix.rebuildIndex(t.Context(), projectID, userID, "", taskID)

	if got := conflicting.attempts.Load(); got != maxIndexSaveAttempts {
		t.Fatalf("save attempts = %d, want %d", got, maxIndexSaveAttempts)
	}
	if got := drainIndexEvents(t, ix, taskID); !slices.Equal(got, []string{"indexing_failed", "task_completed"}) {
		t.Fatalf("events = %v, want [indexing_failed task_completed]", got)
	}
}

func TestRebuildIndex_GivingUpOnACurrentIndexIsNotAFailure(t *testing.T) {
	shared := memory.New()
	const userID, projectID = "alice", "proj-giveup-current"
	winner := &savedByTheWinner{Service: shared}
	seedProjectFile(t, scopedStore(shared), userID, projectID, "a.txt", "alpha content")
	ix, mgr := newTestIndexer(scopedStore(winner))
	const taskID = "77777777-7777-7777-7777-777777777777"
	mgr.RegisterTask(taskID, projectSessionPrefix+projectID, userID)

	ix.rebuildIndex(t.Context(), projectID, userID, "", taskID)

	if got := winner.attempts.Load(); got != maxIndexSaveAttempts {
		t.Fatalf("save attempts = %d, want %d", got, maxIndexSaveAttempts)
	}
	if got := drainIndexEvents(t, ix, taskID); !slices.Equal(got, []string{"task_completed"}) {
		t.Fatalf("events = %v, want [task_completed]", got)
	}
}

func TestRebuildIndex_EmptyProjectSavesEmptyVersion(t *testing.T) {
	shared := memory.New()
	const userID, projectID = "alice", "proj-empty"
	artSvc := scopedStore(shared)
	seedProjectFile(t, artSvc, userID, projectID, "notes.txt", "pluto is a dwarf planet")
	ix := discardIndexer(artSvc)
	ix.rebuildIndex(t.Context(), projectID, userID, "", "")

	if err := shared.Delete(t.Context(), artSvc.Resolve("", userID, projectSessionPrefix+projectID, "notes.txt")); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	ix.rebuildIndex(t.Context(), projectID, userID, "", "")

	latest, sources, data := latestIndex(t, artSvc, userID, projectID)
	if latest != 1 || len(sources.Files) != 0 || sources.IndexedFiles != 0 {
		t.Fatalf("latest = %d, sources = %+v; want an empty version 1", latest, sources)
	}
	if ix.reconcileIndex(t.Context(), projectID, userID, "", data) {
		t.Fatal("an empty index must not be searchable")
	}
}

func waitForIndexVersion(t *testing.T, artSvc *artifact.ScopedService, userID, projectID string, want int, wait time.Duration) bool {
	t.Helper()
	loc := artSvc.Resolve("", userID, projectSessionPrefix+projectID, artifact.BM25IndexFilename)
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if versions, _ := artSvc.Inner.ListVersions(t.Context(), loc); len(versions) >= want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestHandleMessage_StaleIndexStartsRebuild(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := t.Context()

	const owner = "owner_user"
	project := &store.Project{ID: idgen.New(), Name: "Indexed Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	seedProjectFile(t, artSvc, owner, project.ID, "a.txt", "alpha content")

	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	h.indexer = discardIndexer(artSvc)
	h.indexer.rebuildIndex(ctx, project.ID, owner, "", "")

	if rec := sendMessageAs(t, h, "test-agent", project.ID, "", owner); rec.Code != http.StatusOK {
		t.Fatalf("send message: %d %s", rec.Code, rec.Body.String())
	}
	if waitForIndexVersion(t, artSvc, owner, project.ID, 2, 300*time.Millisecond) {
		t.Fatal("a chat on a current index started a rebuild")
	}

	seedProjectFile(t, artSvc, owner, project.ID, "b.txt", "beta content")
	if rec := sendMessageAs(t, h, "test-agent", project.ID, "", owner); rec.Code != http.StatusOK {
		t.Fatalf("send message: %d %s", rec.Code, rec.Body.String())
	}
	if !waitForIndexVersion(t, artSvc, owner, project.ID, 2, 5*time.Second) {
		t.Fatal("a chat on a stale index did not start a rebuild")
	}
	_, sources, _ := latestIndex(t, artSvc, owner, project.ID)
	if want := currentSources(t, artSvc, owner, project.ID); !indexing.SameSourceFiles(sources.Files, want) {
		t.Fatalf("rebuilt index sources = %+v, want %+v", sources.Files, want)
	}
}

type countingSaves struct {
	beforeFirstSaveVersion
	saves atomic.Int32
}

func (s *countingSaves) SaveVersion(ctx context.Context, loc artifact.Locator, version int, content []byte, mimeType string) error {
	s.saves.Add(1)
	return s.beforeFirstSaveVersion.SaveVersion(ctx, loc, version, content, mimeType)
}

func waitForNoRebuild(t *testing.T, ix *projectIndexer, projectID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for ix.inFlight.count(projectID) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("rebuild still in flight")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReconcileIndex_SkipsWhileRebuildInFlight(t *testing.T) {
	shared := memory.New()
	const userID, projectID = "alice", "proj-inflight"
	seedProjectFile(t, scopedStore(shared), userID, projectID, "a.txt", "alpha content")

	counting := &countingSaves{beforeFirstSaveVersion: beforeFirstSaveVersion{Service: shared}}
	ix := discardIndexer(scopedStore(counting))
	counting.hook = func() {
		seedProjectFile(t, ix.artSvc, userID, projectID, "a.txt", "alpha content, edited")
		ix.reconcileIndex(t.Context(), projectID, userID, "", nil)
	}

	ix.triggerIndexRebuild(projectID, userID, "", "")
	waitForNoRebuild(t, ix, projectID)
	if got := counting.saves.Load(); got != 1 {
		t.Fatalf("index saves = %d, want 1 (the chat during the rebuild must not start another)", got)
	}

	_, _, data := latestIndex(t, ix.artSvc, userID, projectID)
	ix.reconcileIndex(t.Context(), projectID, userID, "", data)
	waitForNoRebuild(t, ix, projectID)
	if got := counting.saves.Load(); got != 2 {
		t.Fatalf("index saves = %d, want 2 (a stale chat with no rebuild running starts one)", got)
	}
}
