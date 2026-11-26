// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

func TestAuthzLookupHandler_NonStringGatewayID(t *testing.T) {
	const ns = "rbac-nonstring-gw"
	brk := newTestBroker(t)
	startAuthzHandler(t, brk, newAuthzTestComposer(t), ns)

	resp := roundTrip(t, nil, brk, ns, "alice@example.org", map[string]any{"gateway_id": 42})
	if resp.Error == nil || resp.Error.Code != control.ErrInvalidRequest {
		t.Fatalf("expected invalid-request, got %+v", resp)
	}
}

func TestAuthzLookupHandler_InvalidPayload(t *testing.T) {
	const ns = "rbac-invalid-payload"
	brk := newTestBroker(t)
	startAuthzHandler(t, brk, newAuthzTestComposer(t), ns)

	reqCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reply, err := brk.Request(reqCtx, a2a.AuthzLookupTopic(ns, "webui"), &broker.Message{
		Payload: []byte("not json"),
		ReplyTo: a2a.AuthzResponseTopic(ns, "webui"),
	}, time.Second)
	if err != nil {
		t.Fatalf("broker request: %v", err)
	}
	resp, err := control.UnmarshalResponse(reply.Payload)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != control.ErrInvalidRequest {
		t.Fatalf("expected invalid-request, got %+v", resp)
	}
}

func TestAuthzLookupHandler_CacheHit(t *testing.T) {
	const ns = "rbac-cache-hit"
	brk := newTestBroker(t)
	startAuthzHandler(t, brk, newAuthzTestComposer(t), ns)

	first := roundTrip(t, nil, brk, ns, "alice@example.org", map[string]any{"gateway_id": "webui"})
	if first.Error != nil {
		t.Fatalf("first lookup error: %+v", first.Error)
	}
	second := roundTrip(t, nil, brk, ns, "alice@example.org", map[string]any{"gateway_id": "webui"})
	if second.Error != nil {
		t.Fatalf("second lookup error: %+v", second.Error)
	}
	a, _ := json.Marshal(first.Result)
	b, _ := json.Marshal(second.Result)
	if string(a) != string(b) {
		t.Fatalf("cache hit changed payload: first=%s second=%s", a, b)
	}
}

func TestAuthzLookupHandler_InvalidateAll(t *testing.T) {
	const ns = "rbac-invalidate"
	brk := newTestBroker(t)
	h := NewAuthzLookupHandler(newAuthzTestComposer(t), nil, brk,
		AuthzLookupConfig{Namespace: ns, CacheTTL: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	first := roundTrip(t, nil, brk, ns, "alice@example.org", map[string]any{"gateway_id": "webui"})
	if first.Error != nil {
		t.Fatalf("first lookup error: %+v", first.Error)
	}
	h.InvalidateAll()
	second := roundTrip(t, nil, brk, ns, "alice@example.org", map[string]any{"gateway_id": "webui"})
	if second.Error != nil {
		t.Fatalf("post-invalidate lookup error: %+v", second.Error)
	}
}

func TestCacheKeyFor_ContextSensitivity(t *testing.T) {
	base := map[string]any{"gateway_id": "webui", "idp_claims": map[string]any{"groups": []any{"a"}}}
	withDifferentClaims := map[string]any{"gateway_id": "webui", "idp_claims": map[string]any{"groups": []any{"b"}}}
	withDifferentKeyOrder := map[string]any{"idp_claims": map[string]any{"groups": []any{"a"}}, "gateway_id": "webui"}

	k1 := cacheKeyFor("alice", "webui", base)
	k2 := cacheKeyFor("alice", "webui", withDifferentClaims)
	k3 := cacheKeyFor("alice", "webui", withDifferentKeyOrder)

	if k1 == k2 {
		t.Fatalf("different claims must produce different keys; got %s for both", k1)
	}
	if k1 != k3 {
		t.Fatalf("map iteration order must not change the key: %s != %s", k1, k3)
	}
}

func TestCacheKeyFor_EmptyContext(t *testing.T) {
	k := cacheKeyFor("alice", "webui", nil)
	if k == "" {
		t.Fatal("expected non-empty key for nil context")
	}
	k2 := cacheKeyFor("alice", "webui", map[string]any{})
	if k != k2 {
		t.Fatalf("nil and empty context must hash identically: %s vs %s", k, k2)
	}
}
