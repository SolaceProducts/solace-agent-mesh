// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"io"
	"log/slog"
	"testing"
)

func TestResolveSessionID_FallsBackToWireContextOnMiss(t *testing.T) {
	a := &HTTPSSEAdapter{sseManager: NewSSEManager(100, slog.New(slog.NewTextHandler(io.Discard, nil)))}

	sid, uid := a.resolveSessionID(t.Context(), "task-from-other-pod", "ctx-from-wire")
	if sid != "ctx-from-wire" {
		t.Fatalf("sessionID = %q, want the wire context id on a miss", sid)
	}
	if uid != "" {
		t.Fatalf("userID = %q, want empty on a miss (no wire fallback)", uid)
	}

	a.sseManager.RegisterTask("task-local", "sess-local", "user-local")
	sid, uid = a.resolveSessionID(t.Context(), "task-local", "ctx-from-wire")
	if sid != "sess-local" {
		t.Fatalf("sessionID = %q, want the in-memory session id on a hit", sid)
	}
	if uid != "user-local" {
		t.Fatalf("userID = %q, want the in-memory user id on a hit", uid)
	}
}
