// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

func startUpsertStub(t *testing.T, brk broker.Broker, namespace, gatewayID string, reply func(req map[string]any) *control.Response) *capturedPayload {
	t.Helper()
	queue, err := brk.CreateQueue("stub-upsert-"+namespace+"-"+gatewayID, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create stub queue: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.UserUpsertTopic(namespace, gatewayID)); err != nil {
		t.Fatalf("subscribe stub: %v", err)
	}
	cap := &capturedPayload{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		for {
			msg, err := brk.Receive(ctx, queue)
			if err != nil || msg == nil {
				return
			}
			var payload map[string]any
			_ = json.Unmarshal(msg.Payload, &payload)
			cap.set(payload)
			resp := reply(payload)
			if resp == nil {
				continue
			}
			data, _ := control.MarshalResponse(resp)
			replyTo := msg.ReplyTo
			if replyTo == "" {
				replyTo = a2a.UserUpsertResponseTopic(namespace, gatewayID)
			}
			_ = brk.Publish(ctx, replyTo, &broker.Message{Payload: data})
		}
	}()

	time.Sleep(20 * time.Millisecond)
	return cap
}

type capturedPayload struct {
	mu  sync.Mutex
	got map[string]any
}

func (c *capturedPayload) set(p map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = p
}

func (c *capturedPayload) load() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got
}

func newUpsertClient(brk broker.Broker, namespace, gatewayID string) *BrokerUserUpsertClient {
	return NewBrokerUserUpsertClient(brk, BrokerUserUpsertClientConfig{
		Namespace:   namespace,
		GatewayID:   gatewayID,
		Timeout:     time.Second,
		MaxAttempts: 1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestBrokerUserUpsertClient_SendsPayload(t *testing.T) {
	const ns = "upsert-ok"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	cap := startUpsertStub(t, brk, ns, gw, func(map[string]any) *control.Response {
		return control.SuccessResponse(map[string]any{"id": "u-1"})
	})
	client := newUpsertClient(brk, ns, gw)

	_, err := client.Upsert(t.Context(), UserRecord{
		Issuer:       "https://idp.example.com",
		Sub:          "sub-1",
		OIDCProvider: "azure",
		DisplayName:  "Alice",
		UserName:     "alice",
		Email:        "a@example.com",
		Groups:       []string{"g1", "g2"},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got := cap.load()
	if got == nil {
		t.Fatal("stub received no request")
	}
	if got["issuer"] != "https://idp.example.com" || got["sub"] != "sub-1" || got["email"] != "a@example.com" {
		t.Errorf("unexpected identity fields: %+v", got)
	}
	if got["oidc_provider"] != "azure" {
		t.Errorf("oidc_provider = %v, want azure", got["oidc_provider"])
	}
	if got["display_name"] != "Alice" {
		t.Errorf("display_name = %v, want Alice", got["display_name"])
	}
	if got["user_name"] != "alice" {
		t.Errorf("user_name = %v, want alice", got["user_name"])
	}
	if got["gateway_id"] != gw {
		t.Errorf("gateway_id = %v, want %s", got["gateway_id"], gw)
	}
	var groups []string
	for _, g := range got["groups"].([]any) {
		groups = append(groups, g.(string))
	}
	slices.Sort(groups)
	if !slices.Equal(groups, []string{"g1", "g2"}) {
		t.Errorf("groups = %v, want [g1 g2]", groups)
	}
}

func TestBrokerUserUpsertClient_PropagatesPlatformError(t *testing.T) {
	const ns = "upsert-err"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	startUpsertStub(t, brk, ns, gw, func(map[string]any) *control.Response {
		return control.ErrorResponse(control.ErrInvalidRequest, "phone number already in use")
	})
	client := newUpsertClient(brk, ns, gw)

	_, err := client.Upsert(t.Context(), UserRecord{Issuer: "iss", Sub: "sub"})
	if err == nil {
		t.Fatal("expected an error from a platform error reply")
	}
}

func TestBrokerUserUpsertClient_RetriesTransportFailure(t *testing.T) {
	const ns = "upsert-retry"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	var calls atomic.Int32
	startUpsertStub(t, brk, ns, gw, func(map[string]any) *control.Response {
		if calls.Add(1) == 1 {
			return nil
		}
		return control.SuccessResponse(map[string]any{"id": "u-1"})
	})
	client := NewBrokerUserUpsertClient(brk, BrokerUserUpsertClientConfig{
		Namespace:    ns,
		GatewayID:    gw,
		Timeout:      300 * time.Millisecond,
		MaxAttempts:  2,
		RetryBackoff: 50 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := client.Upsert(t.Context(), UserRecord{Issuer: "iss", Sub: "sub"}); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if got := calls.Load(); got < 2 {
		t.Errorf("expected a retry (>=2 attempts), got %d", got)
	}
}

func TestBrokerUserUpsertClient_RequiresIdentity(t *testing.T) {
	brk := newRemoteAuthzTestBroker(t)
	client := newUpsertClient(brk, "ns", "gw")
	if _, err := client.Upsert(t.Context(), UserRecord{Sub: "sub-only"}); err == nil {
		t.Fatal("expected error when issuer is missing")
	}
	if _, err := client.Upsert(t.Context(), UserRecord{Issuer: "iss-only"}); err == nil {
		t.Fatal("expected error when sub is missing")
	}
}

func TestBrokerUserUpsertClient_ReadsRolesChanged(t *testing.T) {
	const ns = "upsert-roles-changed"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	rolesChanged := true
	startUpsertStub(t, brk, ns, gw, func(map[string]any) *control.Response {
		if rolesChanged {
			return control.SuccessResponse(map[string]any{"id": "u-1", "roles_changed": true})
		}
		return control.SuccessResponse(map[string]any{"id": "u-1"})
	})
	client := newUpsertClient(brk, ns, gw)

	res, err := client.Upsert(t.Context(), UserRecord{Issuer: "iss", Sub: "sub"})
	if err != nil || res != (UserUpsertResult{ID: "u-1", RolesChanged: true}) {
		t.Fatalf("Upsert = %+v, %v; want id u-1 with roles changed", res, err)
	}
	rolesChanged = false
	res, err = client.Upsert(t.Context(), UserRecord{Issuer: "iss", Sub: "sub"})
	if err != nil || res != (UserUpsertResult{ID: "u-1"}) {
		t.Fatalf("Upsert = %+v, %v; want id u-1 without roles changed (a platform that predates the field)", res, err)
	}
}
