// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var sessionDBTemplate *dbtest.Template

func TestMain(m *testing.M) {
	tmpl, err := dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_SESSION_DB_URL"),
		"runtime_session",
		func(u string) error {
			db, openErr := dbutil.Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return session.NewSQLStore(db).Migrate(context.Background())
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest template: %v\n", err)
		os.Exit(1)
	}
	sessionDBTemplate = tmpl
	code := m.Run()
	sessionDBTemplate.Close()
	os.Exit(code)
}

type dbProbeComponent struct {
	db *dbutil.DB
}

func (p *dbProbeComponent) Init(context.Context, config.Config) error { return nil }
func (p *dbProbeComponent) Start(context.Context) error               { return nil }
func (p *dbProbeComponent) Stop(ctx context.Context) error {
	return p.db.RawDB().PingContext(ctx)
}
func (p *dbProbeComponent) Health() error { return nil }

func TestWithSessions_ClosesRealDBPoolOnStop(t *testing.T) {
	db, err := dbutil.Open(sessionDBTemplate.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := session.NewSQLStore(db)

	b := broker.NewDevBroker()
	_ = b.Connect(context.Background())
	rt := NewRuntime(b, nil, WithSessions(store))
	rt.Register("app", &dbProbeComponent{db: db})

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("runtime stop: %v (app component's DB probe failed, or session close errored)", err)
	}

	if err := db.RawDB().PingContext(ctx); err == nil {
		t.Fatal("session store DB pool is still open after Runtime.Stop — its connections will outlive the pod")
	}
}
