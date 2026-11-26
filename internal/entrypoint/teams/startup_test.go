// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newInitialisedAdapter(t *testing.T) (*TeamsAdapter, *fakeBotConnector) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	bot := newFakeBotConnector(t)
	adapter, err := NewTeamsAdapter(map[string]any{
		"app_id":                        twoInstanceAppID,
		"app_password":                  "test-app-password",
		"default_agent_name":            twoInstanceAgent,
		"initial_status_message":        twoInstanceStatus,
		"api_base_url":                  bot.srv.URL + "/i" + strconv.Itoa(0),
		"run_as":                        "default",
		"teams_email_cache_ttl_seconds": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestDBURL(t),
		},
	})
	client := brk.NewClient()
	if err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	arts := &artifact.ScopedService{Inner: memory.New(), Scope: "agent"}
	core.SetServices(runtime.NewRuntime(client, nil, runtime.WithArtifacts(arts)).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: twoInstanceAgent})
	return adapter, bot
}

func TestInit_ClickBeforeStartIsServed(t *testing.T) {
	a, bot := newInitialisedAdapter(t)
	if a.ctx == nil {
		t.Fatal("the adapter has no context after Init, while its webhook is already announced")
	}

	activity := dmActivity(allowClickID)
	delete(activity, "text")
	activity["type"] = ActivityTypeInvoke
	activity["name"] = InvokeFileConsent
	activity["value"] = map[string]any{
		"type": "fileUpload", "action": "accept",
		"context":    map[string]any{"filename": allowedFile, "task_id": "task-gone", "app_name": twoInstanceAgent},
		"uploadInfo": map[string]any{"uploadUrl": "https://upload.invalid/session", "uniqueId": "drive-item-1", "fileType": "pdf"},
	}
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, signedInvokeRequest(t, a.botAuth, a.cfg.AppID, activity))
	if rec.Code != http.StatusOK {
		t.Fatalf("the Allow was answered %d, want 200", rec.Code)
	}
	a.server.Wait()

	want := "Sorry, the file '" + allowedFile + "' is no longer available. It may have expired."
	if got := bot.posted(); !slices.Contains(got, want) {
		t.Errorf("posted = %q, want the refusal %q", got, want)
	}
}

func TestTeamsServer_DispatchedPanicIsRecovered(t *testing.T) {
	logs := &lockedBuffer{}
	s := NewTeamsServer(&TeamsConfig{AppID: twoInstanceAppID}, nil,
		func(context.Context, *Activity) *InvokeResponse { return nil },
		slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if !s.TrackBackground(func() { panic("boom") }) {
		t.Fatal("background work refused on an open server")
	}
	if !s.startDispatch(func() { panic("boom") }) {
		t.Fatal("dispatch refused on an open server")
	}
	s.Wait()

	if n := strings.Count(logs.String(), "panic in dispatched teams work"); n != 2 {
		t.Errorf("recovered panics logged = %d, want 2:\n%s", n, logs.String())
	}
	if n := strings.Count(logs.String(), "stack=\"goroutine "); n != 2 {
		t.Errorf("recovered panics logged with a stack = %d, want 2:\n%s", n, logs.String())
	}
}
