// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func TestWorkflowInvoke_FullPath_RoleConfigToGate(t *testing.T) {
	logger := slog.Default()
	roles := RolesConfig{Roles: map[string]RoleDefinition{
		"wf_runner": {Scopes: []string{"workflow:*:invoke"}},
		"chatter":   {Scopes: []string{"agent:*:delegate"}},
	}}
	users := UsersConfig{Users: map[string]UserAssignment{
		"alice": {Roles: []string{"wf_runner"}},
		"bob":   {Roles: []string{"chatter"}},
	}}
	engine, err := NewRoleEngine(roles, users, nil)
	if err != nil {
		t.Fatal(err)
	}

	aliceScopes := engine.ResolveUserScopes("alice")
	if !slices.Contains(aliceScopes, "workflow:*:invoke") {
		t.Fatalf("wf_runner should resolve workflow:*:invoke, got %v", aliceScopes)
	}
	bobScopes := engine.ResolveUserScopes("bob")

	aa := NewAgentAuth(nil, logger)
	ctx := context.Background()
	alice := &auth.Identity{UserID: "alice", Scopes: aliceScopes}
	bob := &auth.Identity{UserID: "bob", Scopes: bobScopes}

	if err := aa.AuthorizeWorkflow(ctx, alice, "orders_wf"); err != nil {
		t.Errorf("wf_runner should be allowed to invoke a workflow: %v", err)
	}
	if err := aa.AuthorizeAgent(ctx, alice, "some_agent"); err == nil {
		t.Error("workflow:*:invoke must not grant agent delegation")
	}
	if err := aa.AuthorizeWorkflow(ctx, bob, "orders_wf"); err == nil {
		t.Error("agent:*:delegate must not grant workflow invocation")
	}
}

func TestWorkflowInvoke_FullPath_JWTRoundTrip(t *testing.T) {
	logger := slog.Default()
	km, err := trust.NewKeyManager("test-gw")
	if err != nil {
		t.Fatal(err)
	}
	signer := trust.NewSigner(km, "test-gw", time.Hour, 24*time.Hour)
	registry := trust.NewRegistry()
	registry.AddOrUpdate(&trust.TrustCard{
		ComponentType: "gateway",
		ComponentID:   "test-gw",
		Namespace:     "test-ns",
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	verifier := trust.NewVerifier(registry, 5*time.Second, logger)

	token, err := signer.Sign(trust.SignClaims{
		Subject: "alice",
		TaskID:  "task-1",
		Scopes:  []string{"workflow:orders_wf:invoke"},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.Authenticate(context.Background(), token, "task-1")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	aa := NewAgentAuth(nil, logger)
	if err := aa.AuthorizeWorkflow(context.Background(), identity, "orders_wf"); err != nil {
		t.Errorf("scope from the verified JWT should open the workflow gate: %v", err)
	}
	if err := aa.AuthorizeWorkflow(context.Background(), identity, "other_wf"); err == nil {
		t.Error("workflow:orders_wf:invoke must not open a different workflow")
	}
}
