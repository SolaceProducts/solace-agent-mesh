// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestSharedProjectMemberReachesProjectFilesThroughTheAdapterRoutes(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service":       map[string]any{"type": "sql", "database_url": testDBURL(t, "wiring")},
	})
	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	arts := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "test-ns"}
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(flags), runtime.WithArtifacts(arts)).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Store().Close() })
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: ownerTestAgent, URL: "solace:test"})
	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true})

	project := &store.Project{Name: "shared", UserID: createOwnerAlice}
	if err := store.NewProjectRepo(core.Store()).Create(t.Context(), project); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	const projectBytes = "PROJECT-SPEC-BYTES"
	version, err := arts.Inner.Save(t.Context(), arts.Resolve("", createOwnerAlice, projectSessionPrefix+project.ID, "spec.txt"), []byte(projectBytes), "text/plain")
	if err != nil {
		t.Fatalf("save project file: %v", err)
	}
	uri := artifact.Locator{AppName: "test-ns", UserID: createOwnerAlice, SessionID: projectSessionPrefix + project.ID, Filename: "spec.txt"}
	projectURI, err := artifact.FormatURI(uri, version)
	if err != nil {
		t.Fatalf("format URI: %v", err)
	}

	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", ownerTestAgent)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	serve := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SAM-User-ID", createOwnerBob)
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, req)
		return rec
	}

	t.Run("by-uri serves the project's file to the member", func(t *testing.T) {
		rec := serve(http.MethodGet, "/api/v1/artifacts/byUri?uri="+url.QueryEscape(projectURI), nil)
		if rec.Code != http.StatusOK || rec.Body.String() != projectBytes {
			t.Fatalf("status = %d body = %q, want 200 %q", rec.Code, rec.Body.String(), projectBytes)
		}
	})

	t.Run("the message route accepts the member's reference to the project's file", func(t *testing.T) {
		body, marshalErr := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "req1", "params": map[string]any{"message": map[string]any{
			"kind": "message", "role": "user",
			"parts":    []map[string]any{textPart("summarise"), filePart(projectURI)},
			"metadata": map[string]any{"agent_name": ownerTestAgent},
		}}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		rec := serve(http.MethodPost, messageStreamRoute, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		if msg, recvErr := brk.Receive(ctx, q); recvErr != nil || msg == nil {
			t.Fatalf("the member's task was not published: %v", recvErr)
		}
	})
}
