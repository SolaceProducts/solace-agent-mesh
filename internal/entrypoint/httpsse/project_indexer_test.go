// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestPublishIndexEvent_PersistsWithProjectSession(t *testing.T) {
	for _, async := range []bool{false, true} {
		mode := "sync"
		if async {
			mode = "async"
		}
		t.Run(mode, func(t *testing.T) {
			db := store.TestDB(t)
			repo := store.NewSSEEventRepo(db)
			el := eventlog.NewPersistentEventLog(repo, store.NewTaskRepo(db), slog.Default())
			if async {
				el.EnableAsyncFlush(0, 0, 0)
			}

			mgr := NewSSEManager(100, slog.New(slog.NewTextHandler(io.Discard, nil)))
			ix := &projectIndexer{
				logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
				sseManager: mgr,
				eventLog:   el,
			}
			taskID := idgen.New()
			mgr.RegisterTask(taskID, projectSessionPrefix+idgen.New(), idgen.New())

			ix.publishIndexEvent(taskID, map[string]any{"type": "indexing_started"}, false)
			ix.publishIndexEvent(taskID, map[string]any{"type": "task_completed"}, true)

			if async {
				el.Stop()
			}

			rows, err := repo.GetByTask(context.Background(), taskID, 1)
			if err != nil {
				t.Fatalf("GetByTask: %v", err)
			}
			if len(rows) != 2 {
				t.Fatalf("durable rows = %d, want 2 (session_id rejected by UUID column?)", len(rows))
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		name, header, want string
	}{
		{"missing header", "", ""},
		{"non-bearer prefix", "Basic dXNlcjpwYXNz", ""},
		{"empty bearer", "Bearer ", ""},
		{"happy path", "Bearer abc.def.ghi", "abc.def.ghi"},
		{"prefix-only", "Bearer", ""},
		{"case sensitive", "bearer abc.def.ghi", ""},
		{"two spaces", "Bearer  abc.def.ghi", "abc.def.ghi"},
		{"a tab", "Bearer \tabc.def.ghi", "abc.def.ghi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			if got := bearerToken(r); got != c.want {
				t.Errorf("bearerToken() = %q, want %q", got, c.want)
			}
		})
	}
}
