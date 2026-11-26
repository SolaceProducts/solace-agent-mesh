// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func startedPlatform(t *testing.T) *PlatformCore {
	t.Helper()
	return startedPlatformWith(t, ExtensionPoints{}, platformDBURL(t))
}

func platformDBURL(t *testing.T) string {
	return "sqlite:///" + filepath.Join(t.TempDir(), "platform.db")
}

func startedPlatformWith(t *testing.T, ext ExtensionPoints, databaseURL string) *PlatformCore {
	t.Helper()
	core := NewPlatformCore(map[string]any{
		"namespace":    governorWiringNamespace,
		"database_url": databaseURL,
	}, ext)
	core.SetServices(runtime.NewRuntime(connectedDevBroker(t), nil,
		runtime.WithLogger(slog.New(slog.DiscardHandler)),
		runtime.WithFeatureRegistry(featureCatalog(t)),
	).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("platform init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("platform start: %v", err)
	}
	return core
}

func TestSpendPublisher_FilesSpendThroughTheWiredGovernorAndStopsWithThePlatform(t *testing.T) {
	core := startedPlatform(t)
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	publisher := core.SpendPublisher()
	if publisher == nil {
		t.Fatal("no spend publisher")
	}
	if core.SpendPublisher() != publisher {
		t.Error("each call created a publisher, want one shared by the platform's LLM call sites")
	}
	e := spend.Event{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "admin-1",
		Surface: spend.SurfaceAgentGenerator, ModelName: "m",
	}
	publisher.Record(e)
	deadline := time.Now().Add(30 * time.Second)
	for ledgerRows(t, core, e.ID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("spend recorded on the platform's publisher was not filed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := core.Stop(context.Background()); err != nil {
		t.Fatalf("platform stop: %v", err)
	}
	if core.SpendPublisher() != nil {
		t.Error("the platform hands out a spend publisher after it stopped")
	}
}

func ledgerRows(t *testing.T, core *PlatformCore, id string) int {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), core.DB(), `SELECT COUNT(*) FROM token_spend_ledger WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	return n
}

type spendOnStop struct {
	core  *PlatformCore
	event spend.Event
}

func (s *spendOnStop) Start(context.Context) error { return nil }

func (s *spendOnStop) Stop(context.Context) error {
	s.core.SpendPublisher().Record(s.event)
	return nil
}

func TestStop_FilesSpendRecordedWhileTheBackgroundServicesStop(t *testing.T) {
	recorder := &spendOnStop{event: spend.Event{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "system:eval",
		Surface: spend.SurfaceEvalJudge, ModelName: "m",
	}}
	databaseURL := platformDBURL(t)
	core := startedPlatformWith(t, ExtensionPoints{
		BackgroundServices: func(core *PlatformCore) []BackgroundService {
			recorder.core = core
			return []BackgroundService{recorder}
		},
	}, databaseURL)

	if err := core.Stop(context.Background()); err != nil {
		t.Fatalf("platform stop: %v", err)
	}

	db, err := store.Open(databaseURL, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("reopen the platform database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	n, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM token_spend_ledger WHERE id = ?`, recorder.event.ID)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if n != 1 {
		t.Error("spend recorded as the platform stopped was not filed on the in-memory broker")
	}
}
