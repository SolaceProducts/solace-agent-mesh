// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const earlierName = "user:a-earlier.md"

func bubblesReferencingBoth() string {
	var both []any
	for _, name := range []string{earlierName, referencedName} {
		var bubbles []any
		if err := json.Unmarshal([]byte(bubblesReferencing(name)), &bubbles); err != nil {
			panic(err)
		}
		both = append(both, bubbles...)
	}
	raw, err := json.Marshal(both)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestShareLinkAFailureOnAnotherNamesLookupDoesNotChangeTheRequestedFilesAnswer(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencingBoth())
	f.save(shareSharer, savedElsewhere, earlierName, "EARLIER-BYTES")
	failing := earlierName
	f.backend.failVersionsOf.Store(&failing)

	rec := f.open(shareViewer, referencedName)
	if rec.Code != http.StatusOK || rec.Body.String() != referencedBytes {
		t.Errorf("the requested file: %d %q, want 200 %q although another name's lookup fails", rec.Code, rec.Body.String(), referencedBytes)
	}
	if got := f.logged("ERROR"); len(got) != 0 {
		t.Errorf("errors = %v, want none for a request that did not touch the failing name", got)
	}
	for _, line := range f.logged("WARN") {
		if strings.Contains(line["msg"].(string), "refused") {
			t.Errorf("a refusal was logged for a file that was served: %v", line)
		}
	}

	if got := f.backend.versionLookups(earlierName); got != 0 {
		t.Errorf("%d version lookups of a name the request did not ask for, want 0", got)
	}

	rec = f.open(shareViewer, earlierName)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("the name whose lookup fails: %d, want 500", rec.Code)
	}
	if got := f.logged("ERROR"); len(got) != 1 {
		t.Errorf("errors = %v, want one for the request that hit the outage", got)
	}
}

func TestShareArtifactScopeAnswersANameTheSameWayAfterAnotherNameFails(t *testing.T) {
	backend := &flakyArtifactBackend{Service: filesystem.New(t.TempDir())}
	arts := &artifact.ScopedService{Inner: backend, Scope: "namespace", Namespace: "test-ns"}
	for _, name := range []string{referencedName, earlierName} {
		if _, err := arts.Inner.Save(t.Context(), arts.Resolve("", shareSharer, "sess-1", name), []byte("x"), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	failing := earlierName
	backend.failVersionsOf.Store(&failing)
	scope := &shareArtifactScope{
		requester: shareViewer,
		link:      &store.SharedLink{ShareID: "share-1", UserID: shareSharer, SessionID: "sess-1"},
		artifacts: arts,
		logger:    (&warnRecorder{}).logger(),
		load: func() (entrypoint.ReferencedUserFiles, error) {
			return entrypoint.ReferencedUserFiles{referencedName: 0, earlierName: 0}, nil
		},
	}

	if scope.refuses(t.Context(), referencedName) {
		t.Fatal("the referenced file is refused before any failure")
	}
	if !scope.refuses(t.Context(), earlierName) || scope.unavailable() == nil {
		t.Fatal("the name whose lookup fails is admitted, or the failure is not kept")
	}
	if scope.refuses(t.Context(), referencedName) {
		t.Error("a name decided before the failure is refused after it")
	}
}

func TestShareLinkForkRecognisesEveryForkMarkerThatEverShipped(t *testing.T) {
	for name, metadata := range map[string]string{
		"camelCase keys":     `{"forkedFromOwnerId":"user-2","forkedFrom":"t1","forkedFromSessionId":"s1"}`,
		"snake_case keys":    `{"forked_from_owner_id":"user-2","forked_from":"t1","forked_from_session_id":"s1"}`,
		"one snake_case key": `{"forked_from":"t1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
			f.addTask(shareSharer, bubblesReferencing(unreferencedName), &metadata)
			if rec := f.open(shareViewer, unreferencedName); rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 for a file only a forked-in message references", rec.Code)
			}
		})
	}
}

func TestShareLinkAForkByTheSharerLeavesHerUserScopedFilesAtTheirVersions(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))

	f.fork(shareSharer)
	f.fork(shareSharer)

	for _, name := range []string{referencedName, unreferencedName} {
		versions, err := f.arts.Inner.ListVersions(t.Context(), f.arts.Resolve("", shareSharer, f.sessionID, name))
		if err != nil || !slices.Equal(versions, []int{0}) {
			t.Errorf("%s has versions %v (err %v) after the sharer forked her chat, want [0]", name, versions, err)
		}
	}
	if rec := f.open(shareViewer, referencedName); rec.Code != http.StatusOK || rec.Body.String() != referencedBytes {
		t.Errorf("the viewer opens the referenced file after the sharer forked: %d %q, want 200", rec.Code, rec.Body.String())
	}
}

func TestShareArtifactScopeWithoutAnArtifactStoreAdmitsNoUserScopedName(t *testing.T) {
	scope := &shareArtifactScope{
		requester: shareViewer,
		link:      &store.SharedLink{ShareID: "share-1", UserID: shareSharer, SessionID: "sess-1"},
		logger:    (&warnRecorder{}).logger(),
		load: func() (entrypoint.ReferencedUserFiles, error) {
			return entrypoint.ReferencedUserFiles{referencedName: 0}, nil
		},
	}
	if !scope.refuses(t.Context(), referencedName) {
		t.Error("a referenced name is admitted although there is no artifact store to hold it")
	}
	if scope.unavailable() != nil {
		t.Error("a missing artifact store is reported as an outage")
	}
}

func TestShareLinkLoadsTheVersionTheChatNames(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))

	if rec := f.open(shareViewer, referencedName); rec.Code != http.StatusOK {
		t.Fatalf("open: %d", rec.Code)
	}
	if got := f.backend.versionLoaded(referencedName); got == nil || *got != 0 {
		t.Errorf("the by-name route loaded version %v, want 0", got)
	}
	if rec := f.open(shareViewer, sessionScopedName); rec.Code != http.StatusOK {
		t.Fatalf("open an ordinary file: %d", rec.Code)
	}
	if got := f.backend.versionLoaded(sessionScopedName); got != nil {
		t.Errorf("an ordinary file was loaded at version %v, want its latest", *got)
	}
}

func TestCopySessionArtifactsLoadsAPinnedVersion(t *testing.T) {
	arts := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	for _, content := range []string{"first", "second"} {
		if _, err := arts.Inner.Save(t.Context(), arts.Resolve("", shareSharer, "sess-1", referencedName), []byte(content), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}

	copySessionArtifacts(t.Context(), forkCopy{artSvc: arts, srcUserID: shareSharer, srcSessionID: "sess-1", dstUserID: shareViewer, dstSessionID: "sess-2", logger: (&warnRecorder{}).logger()}, []string{referencedName}, map[string]int{referencedName: 0})

	got, _, err := arts.Inner.Load(t.Context(), arts.Resolve("", shareViewer, "sess-2", referencedName), nil)
	if err != nil || string(got) != "first" {
		t.Errorf("the copy holds %q (err %v), want the pinned version %q", got, err, "first")
	}
}

type savedFiles struct {
	artifact.Service
	saved []string
}

func (s *savedFiles) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	if !strings.HasSuffix(loc.Filename, artifact.MetadataSuffix) {
		s.saved = append(s.saved, loc.UserID+"/"+loc.Filename)
	}
	return s.Service.Save(ctx, loc, content, mimeType)
}

func TestCopySessionArtifactsSkipsAUserScopedFileOnlyForTheSameUserId(t *testing.T) {
	for _, tc := range []struct {
		name, dstUser string
		wantSaved     []string
	}{
		{"the same id", shareSharer, []string{shareSharer + "/" + sessionScopedName}},
		{"an id that differs by ASCII case", "USER-1", []string{"USER-1/" + sessionScopedName, "USER-1/" + referencedName}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &savedFiles{Service: filesystem.New(t.TempDir())}
			arts := &artifact.ScopedService{Inner: backend, Scope: "namespace", Namespace: "test-ns"}
			for _, name := range []string{sessionScopedName, referencedName} {
				if _, err := backend.Service.Save(t.Context(), arts.Resolve("", shareSharer, "sess-1", name), []byte("x"), "text/plain"); err != nil {
					t.Fatal(err)
				}
			}

			copySessionArtifacts(t.Context(), forkCopy{artSvc: arts, srcUserID: shareSharer, srcSessionID: "sess-1", dstUserID: tc.dstUser, dstSessionID: "sess-2", logger: (&warnRecorder{}).logger()}, []string{sessionScopedName, referencedName}, nil)

			if !slices.Equal(backend.saved, tc.wantSaved) {
				t.Errorf("saved %v, want %v", backend.saved, tc.wantSaved)
			}
		})
	}
}

func TestShareLinkAForkByACaseTwinOfTheSharerCopiesIntoItsOwnScope(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))

	got := f.fork("USER-1")
	if !slices.Contains(got, referencedName) {
		t.Errorf("fork holds %v, want the user-scoped file copied into the twin's own scope", got)
	}
}

func TestForkedTaskMetadataWritesOnlyMarkersTheRuleRecognises(t *testing.T) {
	src := &store.ChatTask{ID: "t1", TaskMetadata: strPtr(`{"forked_from":"old","forked_from_owner_id":"x","status":"done","schemaVersion":1}`)}
	raw := forkedTaskMetadata(src, "sess-1", shareSharer)
	var written map[string]any
	if err := json.Unmarshal([]byte(*raw), &written); err != nil {
		t.Fatal(err)
	}
	for key := range written {
		if strings.HasPrefix(key, "forked") && !entrypoint.IsForkMarkerKey(key) {
			t.Errorf("forkedTaskMetadata writes %q, which entrypoint.IsForkMarkerKey does not recognise", key)
		}
	}
	if _, stale := written["forked_from"]; stale {
		t.Error("a legacy marker of the source survived into the copy")
	}
	for _, key := range []string{"forkedFrom", "forkedFromSessionId", entrypoint.ForkedFromOwnerKey} {
		if _, ok := written[key]; !ok {
			t.Errorf("forkedTaskMetadata does not write %q", key)
		}
	}
}

func TestShareLinkAVersionWrittenBetweenTheDecisionAndTheLoadIsNotServed(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	writeLater := func(loc artifact.Locator) {
		if _, err := f.backend.Save(t.Context(), loc, []byte(laterVersionBytes), "text/plain"); err != nil {
			t.Errorf("write the later version: %v", err)
		}
	}
	f.backend.afterVersionLookup.Store(&writeLater)

	rec := f.open(shareViewer, referencedName)
	if rec.Code != http.StatusOK || rec.Body.String() != referencedBytes {
		t.Errorf("open: %d %q, want 200 with the version the chat names (%q)", rec.Code, rec.Body.String(), referencedBytes)
	}
}

func TestShareLinkAVersionWrittenBetweenTheDecisionAndTheCopyIsNotForked(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	writeLater := func(loc artifact.Locator) {
		if _, err := f.backend.Save(t.Context(), loc, []byte(laterVersionBytes), "text/plain"); err != nil {
			t.Errorf("write the later version: %v", err)
		}
	}
	f.backend.afterVersionLookup.Store(&writeLater)

	rec := f.forkAs(shareViewer)
	var forked ForkSharedChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &forked); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("fork: %d %s", rec.Code, rec.Body.String())
	}
	got, _, err := f.arts.Inner.Load(t.Context(), f.arts.Resolve("", shareViewer, forked.SessionID, referencedName), nil)
	if err != nil || string(got) != referencedBytes {
		t.Errorf("the forked copy holds %q (err %v), want the version the chat names (%q)", got, err, referencedBytes)
	}
}

func TestShareArtifactScopePinsOnlyANameItAdmittedOnTheChatsStrength(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	h, link := f.h, &store.SharedLink{ShareID: "share-1", UserID: shareSharer, SessionID: f.sessionID}
	scope := h.scopeFromStoredTasks(t.Context(), shareViewer, link)

	if got := scope.pinnedVersion(referencedName); got != nil {
		t.Errorf("pinned %d before any decision, want nil", *got)
	}
	if scope.refuses(t.Context(), referencedName) {
		t.Fatal("the referenced name is refused")
	}
	if got := scope.pinnedVersion(referencedName); got == nil || *got != 0 {
		t.Errorf("pinned %v after admitting the referenced name, want 0", got)
	}
	if scope.refuses(t.Context(), unreferencedName) != true || scope.pinnedVersion(unreferencedName) != nil {
		t.Error("a refused name is pinned")
	}
	if got := scope.pinnedVersion(sessionScopedName); got != nil {
		t.Errorf("an ordinary file is pinned at %d, want its latest", *got)
	}
}

type missingOnLoad struct {
	artifact.Service
	missing string
}

func (m *missingOnLoad) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	if loc.Filename == m.missing {
		return nil, "", nil
	}
	return m.Service.Load(ctx, loc, version)
}

func TestCopySessionArtifactsSkipsAGoneFileAndCopiesAnEmptyOne(t *testing.T) {
	recorded := &savedFiles{Service: filesystem.New(t.TempDir())}
	arts := &artifact.ScopedService{Inner: &missingOnLoad{Service: recorded, missing: sessionScopedName}, Scope: "namespace", Namespace: "test-ns"}
	for name, content := range map[string]string{sessionScopedName: "x", "other.txt": "x", "empty.txt": ""} {
		if _, err := recorded.Service.Save(t.Context(), arts.Resolve("", shareSharer, "sess-1", name), []byte(content), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	logs := &warnRecorder{}

	copied, names := copySessionArtifacts(t.Context(), forkCopy{artSvc: arts, srcUserID: shareSharer, srcSessionID: "sess-1", dstUserID: shareViewer, dstSessionID: "sess-2", logger: logs.logger()}, []string{sessionScopedName, "other.txt", "empty.txt"}, nil)

	if copied != 2 || !slices.Equal(names, []string{"other.txt", "empty.txt"}) {
		t.Errorf("copied %d %v, want other.txt and the empty file empty.txt, and not the missing file", copied, names)
	}
	if empty, _, err := arts.Inner.Load(t.Context(), arts.Resolve("", shareViewer, "sess-2", "empty.txt"), nil); err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("the copy of the empty file = %#v (err %v), want an empty file", empty, err)
	}
	if slices.Contains(recorded.saved, shareViewer+"/"+sessionScopedName) {
		t.Errorf("saved %v, want no empty copy of the missing file", recorded.saved)
	}
	if !strings.Contains(logs.text(), "source artifact for fork copy is gone") {
		t.Errorf("the skip was not logged: %s", logs.text())
	}
}

func TestCopySessionArtifactsKeepsTheCompanionOfAnOrdinaryFile(t *testing.T) {
	arts := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	logger := (&warnRecorder{}).logger()
	opts := artifact.MetadataOpts{Description: "the notes", Tags: []string{"__working", "draft"}, Custom: map[string]any{"origin": "the-source-chat"}}
	if _, err := artifact.SaveWithMetadata(t.Context(), arts, "", shareSharer, "sess-1", sessionScopedName, []byte("x"), "text/plain", opts, logger); err != nil {
		t.Fatal(err)
	}
	if _, err := artifact.SaveWithMetadata(t.Context(), arts, "", shareViewer, "sess-2", sessionScopedName, []byte("y"), "text/plain", artifact.MetadataOpts{Description: "a decoy"}, logger); err != nil {
		t.Fatal(err)
	}

	copySessionArtifacts(t.Context(), forkCopy{artSvc: arts, srcUserID: shareSharer, srcSessionID: "sess-1", dstUserID: shareViewer, dstSessionID: "sess-3", logger: logger}, []string{sessionScopedName}, nil)

	copied := artifact.LoadCompanionMetadata(t.Context(), arts, "", shareViewer, "sess-3", sessionScopedName, nil)
	if copied["description"] != "the notes" || copied["origin"] != "the-source-chat" {
		t.Errorf("the copy's companion = %v, want the source's description and custom field", copied)
	}
	if tags, _ := copied["tags"].([]any); len(tags) != 2 || tags[0] != "__working" || tags[1] != "draft" {
		t.Errorf("the copy's tags = %v, want the source's", copied["tags"])
	}
}

func TestCopySessionArtifactsReadsTheCompanionOfThePinnedVersion(t *testing.T) {
	arts := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	logger := (&warnRecorder{}).logger()
	for _, description := range []string{"first description", "second description"} {
		if _, err := artifact.SaveWithMetadata(t.Context(), arts, "", shareSharer, "sess-1", referencedName, []byte(description), "text/plain", artifact.MetadataOpts{Description: description}, logger); err != nil {
			t.Fatal(err)
		}
	}

	copySessionArtifacts(t.Context(), forkCopy{artSvc: arts, srcUserID: shareSharer, srcSessionID: "sess-1", dstUserID: shareViewer, dstSessionID: "sess-2", logger: logger}, []string{referencedName}, map[string]int{referencedName: 0})

	copied := artifact.LoadCompanionMetadata(t.Context(), arts, "", shareViewer, "sess-2", referencedName, nil)
	if copied["description"] != "first description" {
		t.Errorf("the copy's description = %v, want that of the pinned version", copied["description"])
	}
}

type genericTypeBackend struct {
	artifact.Service
	metadataVersions []*int
}

func (g *genericTypeBackend) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	content, _, err := g.Service.Load(ctx, loc, version)
	return content, "application/octet-stream", err
}

func (g *genericTypeBackend) GetVersionMetadata(ctx context.Context, loc artifact.Locator, version *int) (*artifact.VersionMetadata, error) {
	g.metadataVersions = append(g.metadataVersions, version)
	return g.Service.GetVersionMetadata(ctx, loc, version)
}

func TestCopySessionArtifactsReadsTheTypeOfThePinnedVersion(t *testing.T) {
	backend := &genericTypeBackend{Service: filesystem.New(t.TempDir())}
	arts := &artifact.ScopedService{Inner: backend, Scope: "namespace", Namespace: "test-ns"}
	for _, content := range []string{"first", "second"} {
		if _, err := backend.Save(t.Context(), arts.Resolve("", shareSharer, "sess-1", referencedName), []byte(content), "text/markdown"); err != nil {
			t.Fatal(err)
		}
	}

	copySessionArtifacts(t.Context(), forkCopy{artSvc: arts, srcUserID: shareSharer, srcSessionID: "sess-1", dstUserID: shareViewer, dstSessionID: "sess-2", logger: (&warnRecorder{}).logger()}, []string{referencedName}, map[string]int{referencedName: 0})

	if len(backend.metadataVersions) == 0 || backend.metadataVersions[0] == nil || *backend.metadataVersions[0] != 0 {
		t.Errorf("the type was read at versions %v, want the pinned version 0", backend.metadataVersions)
	}
}

func TestForkableArtifactsLeavesOutInternalCompanions(t *testing.T) {
	arts := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	for _, name := range []string{"x.txt", "x.txt" + artifact.MetadataSuffix} {
		if _, err := arts.Inner.Save(t.Context(), arts.Resolve("", shareSharer, "sess-1", name), []byte("x"), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}

	keys, refused, err := forkableArtifacts(t.Context(), arts, shareSharer, "sess-1", func(string) bool { return false })

	if err != nil || refused != 0 || !slices.Equal(keys, []string{"x.txt"}) {
		t.Errorf("keys = %v, refused = %d, err = %v, want only x.txt", keys, refused, err)
	}
}

func TestShareLinkForkWithoutAnArtifactStoreCopiesTheChatOnly(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	f.h.artSvc = nil

	rec := f.forkAs(shareViewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("fork: %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := f.sessionCount(shareViewer); got != 1 {
		t.Errorf("sessions = %d, want the forked chat", got)
	}
	if got := f.listing(shareViewer); !slices.Equal(got, []string{}) {
		t.Errorf("viewer is shown %v, want no artifacts", got)
	}
}
