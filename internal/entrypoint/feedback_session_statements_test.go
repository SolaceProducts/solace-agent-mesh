// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type countingConnector struct {
	drv driver.Driver
	dsn string
	n   *atomic.Int64
}

func (c countingConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.drv.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return countingConn{Conn: conn, n: c.n}, nil
}

func (c countingConnector) Driver() driver.Driver { return c.drv }

type countingConn struct {
	driver.Conn
	n *atomic.Int64
}

func (c countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.n.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.n.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func countStatements(t *testing.T, db *store.DB) *atomic.Int64 {
	t.Helper()
	if db.Dialect() == "postgres" {
		t.Skip("the counting wrapper reopens the SQLite file; the statement count does not depend on the dialect")
	}
	var file string
	if err := db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file); err != nil {
		t.Fatalf("database file: %v", err)
	}
	n := &atomic.Int64{}
	counted := sql.OpenDB(countingConnector{drv: db.Driver(), dsn: "file:" + file + "?_pragma=busy_timeout(5000)", n: n})
	counted.SetMaxOpenConns(1)
	original := db.DB.DB
	db.DB.DB = counted
	t.Cleanup(func() {
		db.DB.DB = original
		_ = counted.Close()
	})
	return n
}

func longSession(t *testing.T, exe *GatewayExecutor, turns int) seededSession {
	t.Helper()
	specs := make([]sessionTask, 0, 2*turns+1)
	for i := range turns {
		specs = append(specs, sessionTask{subject: uuidA, parent: -1, start: 1000 + int64(i)*10})
	}
	for i := range turns {
		specs = append(specs, sessionTask{subject: uuidB, parent: i, start: 1000 + int64(i)*10 + 1})
	}
	middle := turns / 2
	specs = append(specs, sessionTask{subject: uuidB, parent: turns + middle, start: 1000 + int64(middle)*10 + 2})
	s := seedFeedbackSession(t, exe, specs, turns-1, uuidA)
	seedForwardedPeerEvent(t, exe.Gateways()[0].Store(), s.taskIDs[middle], uuidA, uuidB)
	return s
}

func TestFeedbackSession_StatementCountIndependentOfTurns(t *testing.T) {
	const maxStatements = 8
	callers := []struct {
		name     string
		identity *auth.Identity
	}{
		{"trace", identityWith("reader", rbac.AgentReadFeedbackScope(uuidA), rbac.AgentReadTraceScope(uuidA), rbac.AgentReadTraceScope(uuidB))},
		{"admin", &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}},
		{"owner", identityWith("u1")},
	}
	for _, c := range callers {
		t.Run(c.name, func(t *testing.T) {
			counts := map[int]int64{}
			for _, turns := range []int{5, analytics.MaxSessionTurns} {
				exe := newStoreBackedActivityExec(t)
				s := longSession(t, exe, turns)
				n := countStatements(t, exe.Gateways()[0].Store())
				got, err := callFeedbackSession(t, exe, s.feedbackID, c.identity)
				if err != nil {
					t.Fatalf("%d turns: %v", turns, err)
				}
				if len(got.Turns) != turns {
					t.Fatalf("%d turns: served %d", turns, len(got.Turns))
				}
				counts[turns] = n.Load()
			}
			if counts[5] != counts[analytics.MaxSessionTurns] {
				t.Errorf("statements grew with the session: 5 turns=%d, %d turns=%d", counts[5], analytics.MaxSessionTurns, counts[analytics.MaxSessionTurns])
			}
			if counts[analytics.MaxSessionTurns] > maxStatements {
				t.Errorf("statements = %d, want at most %d", counts[analytics.MaxSessionTurns], maxStatements)
			}
			t.Logf("statements: %v", counts)
		})
	}
}

func TestFeedbackSession_DeepRatedTaskFindsItsTurn(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	specs := []sessionTask{{subject: uuidA, parent: -1, start: 1000}}
	for d := range analytics.MaxTreeWalkUpHops + 3 {
		specs = append(specs, sessionTask{subject: uuidA, parent: d, start: 1001 + int64(d)})
	}
	s := seedFeedbackSession(t, exe, specs, len(specs)-1, uuidA)

	got, err := callFeedbackSession(t, exe, s.feedbackID, &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}})
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	if got.RetentionGap || len(got.Turns) != 1 || got.Turns[0].Root.ID != s.taskIDs[0] {
		t.Fatalf("gap=%v turns=%d, want the rated turn found", got.RetentionGap, len(got.Turns))
	}
}
