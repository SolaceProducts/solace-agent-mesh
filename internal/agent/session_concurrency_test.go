// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func newSQLiteSessionStore(t *testing.T) session.Store {
	t.Helper()
	url := "sqlite:///" + filepath.Join(t.TempDir(), "sess.db")
	db, err := dbutil.Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := session.NewSQLStore(db)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func concUserMessage(text, contextID string) a2a.Message {
	return a2a.Message{
		Kind:      "message",
		MessageID: "m-" + text,
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart(text)},
		ContextID: contextID,
	}
}

func TestConcurrentTurnsDoNotLoseHistory(t *testing.T) {
	store := newSQLiteSessionStore(t)
	agent, _, _ := newTestAgent()
	agent.svc.(*mockServices).sessions = store

	const ctxID = "ctx-concurrency"
	up := a2a.UserProperties{UserID: "u1", ClientID: "c1"}

	if _, err := agent.HandleTask(t.Context(), "task-0", concUserMessage("turn-zero", ctxID), up); err != nil {
		t.Fatalf("turn 0: %v", err)
	}

	const N = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 1; i <= N; i++ {
		wg.Go(func() {
			<-start
			_, _ = agent.HandleTask(context.Background(), fmt.Sprintf("task-%d", i), concUserMessage(fmt.Sprintf("fact-%d", i), ctxID), up)
		})
	}
	close(start)
	wg.Wait()

	sess, err := store.Get(context.Background(), agent.cfg.Name, "u1", ctxID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	present := make(map[int]bool)
	for _, m := range sess.Messages {
		if m.Role != "user" {
			continue
		}
		for i := 1; i <= N; i++ {
			if strings.Contains(m.Content, fmt.Sprintf("fact-%d", i)) {
				present[i] = true
			}
		}
	}
	var missing []int
	for i := 1; i <= N; i++ {
		if !present[i] {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("lost %d/%d concurrent turns to the session save race: missing %v (persisted %d messages, version %d)",
			len(missing), N, missing, len(sess.Messages), sess.Version)
	}
}
