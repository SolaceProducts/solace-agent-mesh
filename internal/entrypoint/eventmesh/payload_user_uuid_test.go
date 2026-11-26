// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type platformUserMatchingResolver struct {
	*fakeScopeResolver
	platformUserID string
	lookups        int
}

func (r *platformUserMatchingResolver) ResolveScopesAndPlatformUser(userID string, roles []string) ([]string, string) {
	r.lookups++
	return r.ResolveScopes(userID, roles), r.platformUserID
}

func TestProcessIncomingMessage_PayloadNamedUserGetsNoPlatformUserRow(t *testing.T) {
	const victimRow = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c47"
	adapter, brk, scopes := setupAdapterForAuth(t, defaultAuthAdapterOpts())
	resolver := &platformUserMatchingResolver{fakeScopeResolver: scopes, platformUserID: victimRow}
	adapter.core.SetScopeResolver(resolver)
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test", "TestAgent")); err != nil {
		t.Fatalf("subscribe topic: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], &broker.Message{
		Topic: "in/orders", Payload: payload, UserProperties: map[string]string{"user_id": "victim@example.com"},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	got, _ := brk.Receive(ctx, queue)
	if got == nil {
		t.Fatal("expected the event to publish as the payload-named user, got none")
	}
	if resolver.lookups == 0 {
		t.Fatal("the matching lookup was never consulted, so the test proves nothing")
	}
	if props := a2a.DecodeUserProperties(got.UserProperties); props.UserID != "victim@example.com" || props.UserUUID != "" {
		t.Errorf("task ran as %q with user uuid %q, want victim@example.com with none", props.UserID, props.UserUUID)
	}
}
