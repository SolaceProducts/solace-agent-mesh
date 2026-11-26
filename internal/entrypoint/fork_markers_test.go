// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestUserScopedFilesReferencedByNamesAtMostTheFirstFewUnreadableTasks(t *testing.T) {
	var tasks []*store.ChatTask
	for i := range 12 {
		tasks = append(tasks, &store.ChatTask{ID: "bad-" + strconv.Itoa(i), UserID: "alice", MessageBubbles: `[{`})
	}
	_, err := UserScopedFilesReferencedBy(tasks, "alice")
	if !errors.Is(err, ErrUnreadableTasks) || !strings.Contains(err.Error(), "12 task(s)") || !strings.Contains(err.Error(), "bad-4") || strings.Contains(err.Error(), "bad-5") {
		t.Errorf("err = %v, want the count and only the first five ids", err)
	}
}

func TestEveryForkMarkerKeyAloneMarksAForkCopyAndIsPreserved(t *testing.T) {
	for _, key := range []string{"forkedFromOwnerId", "forkedFrom", "forkedFromSessionId", "forked_from_owner_id", "forked_from", "forked_from_session_id"} {
		t.Run(key, func(t *testing.T) {
			if !IsForkMarkerKey(key) {
				t.Fatalf("IsForkMarkerKey(%q) = false", key)
			}
			metadata := `{"` + key + `":"x"}`
			task := &store.ChatTask{ID: "t", UserID: "alice", MessageBubbles: `[{"text":"«artifact:user:a.md:1»"}]`, TaskMetadata: &metadata}
			if refs, err := UserScopedFilesReferencedBy([]*store.ChatTask{task}, "alice"); err != nil || len(refs) != 0 {
				t.Errorf("a task marked only by %q vouches: %v (err %v)", key, refs, err)
			}
			incoming := `{"status":"completed"}`
			preserved := PreserveForkMarkers(&metadata, &incoming)
			var got map[string]any
			if err := json.Unmarshal([]byte(*preserved), &got); err != nil || got[key] != "x" {
				t.Errorf("preserved metadata = %s, want %q kept", *preserved, key)
			}
		})
	}
	if IsForkMarkerKey("status") || IsForkMarkerKey("forked") {
		t.Error("a key that is no fork marker is recognised")
	}
}

func TestPreserveForkMarkersWritesExactlyTheMergedObject(t *testing.T) {
	stored := `{"forkedFromOwnerId":"mallory"}`
	incoming := `{"a":1}`
	if got := *PreserveForkMarkers(&stored, &incoming); got != `{"a":1,"forkedFromOwnerId":"mallory"}` {
		t.Errorf("preserved metadata = %s", got)
	}
}

func TestPreserveForkMarkersKeepsTheClientsNumbersAndKeysAsTheyWere(t *testing.T) {
	stored := `{"forkedFromOwnerId":"mallory"}`
	incoming := `{"big":12345678901234567890,"html":"<b>"}`
	preserved := PreserveForkMarkers(&stored, &incoming)
	if !strings.Contains(*preserved, `"big":12345678901234567890`) || !strings.Contains(*preserved, `"html":"<b>"`) {
		t.Errorf("preserved metadata = %s, want the client's values byte for byte", *preserved)
	}
}

func TestPreserveForkMarkers(t *testing.T) {
	ptr := func(s string) *string { return &s }
	decode := func(t *testing.T, raw *string) map[string]any {
		t.Helper()
		var m map[string]any
		if raw == nil {
			t.Fatal("metadata is nil")
		}
		if err := json.Unmarshal([]byte(*raw), &m); err != nil {
			t.Fatalf("metadata %q: %v", *raw, err)
		}
		return m
	}

	t.Run("a re-save keeps every marker the stored task carries", func(t *testing.T) {
		stored := ptr(`{"forkedFromOwnerId":"mallory","forkedFrom":"t1","forked_from_session_id":"s1","status":"pending"}`)
		got := decode(t, PreserveForkMarkers(stored, ptr(`{"schemaVersion":1,"status":"completed"}`)))
		if got["forkedFromOwnerId"] != "mallory" || got["forkedFrom"] != "t1" || got["forked_from_session_id"] != "s1" {
			t.Errorf("markers lost: %v", got)
		}
		if got["status"] != "completed" || got["schemaVersion"] != float64(1) {
			t.Errorf("the client's own keys were not kept: %v", got)
		}
	})
	t.Run("a marker the client sends cannot change the stored one", func(t *testing.T) {
		got := decode(t, PreserveForkMarkers(ptr(`{"forkedFromOwnerId":"mallory"}`), ptr(`{"forkedFromOwnerId":"alice"}`)))
		if got["forkedFromOwnerId"] != "mallory" {
			t.Errorf("forkedFromOwnerId = %v, want the stored one", got["forkedFromOwnerId"])
		}
	})
	t.Run("a re-save with no metadata, or metadata that is not an object, keeps the markers alone", func(t *testing.T) {
		for _, incoming := range []*string{nil, ptr(""), ptr("not json"), ptr("[1]"), ptr("null")} {
			got := decode(t, PreserveForkMarkers(ptr(`{"forkedFromOwnerId":"mallory","status":"x"}`), incoming))
			if len(got) != 1 || got["forkedFromOwnerId"] != "mallory" {
				t.Errorf("incoming %v: metadata = %v, want only the marker", incoming, got)
			}
		}
	})
	t.Run("a task that is no fork is saved as the client sent it", func(t *testing.T) {
		incoming := ptr(`{"status":"completed"}`)
		for _, stored := range []*string{nil, ptr(""), ptr(`{"status":"pending"}`), ptr("not json")} {
			if got := PreserveForkMarkers(stored, incoming); got != incoming {
				t.Errorf("stored %v: metadata = %v, want the incoming pointer unchanged", stored, got)
			}
		}
	})
}

func TestLatestArtifactVersion(t *testing.T) {
	arts := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	for range 3 {
		if _, err := arts.Inner.Save(t.Context(), arts.Resolve("", "alice", "s1", "f.txt"), []byte("x"), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := LatestArtifactVersion(t.Context(), arts, "alice", "s1", "f.txt"); err != nil || got != 2 {
		t.Errorf("latest version = %d (err %v), want 2", got, err)
	}
	if got, err := LatestArtifactVersion(t.Context(), arts, "alice", "s1", "missing.txt"); err != nil || got != -1 {
		t.Errorf("a missing file: %d (err %v), want -1", got, err)
	}
}
