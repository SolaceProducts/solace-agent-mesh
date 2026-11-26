// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestAgentNameResolverAdapter_BuiltinShortCircuits(t *testing.T) {
	t.Parallel()
	r := &agentNameResolverAdapter{repo: nil}
	for _, name := range []string{"Builder"} {
		got, err := r.ResolveAgentRoutingName(t.Context(), name)
		if err != nil {
			t.Fatalf("%s: unexpected err: %v", name, err)
		}
		if got != name {
			t.Fatalf("%s: got %q, want verbatim", name, got)
		}
	}
}

func TestAgentNameResolverAdapter_DBHitReturnsSlug(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewAgentRepo(db)
	desc := "platform-managed agent under test"
	prompt := "system prompt"
	agent := &store.Agent{
		Name:         "MyPlatformAgent",
		Type:         "standard",
		Description:  &desc,
		SystemPrompt: &prompt,
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := repo.Create(t.Context(), agent, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	r := &agentNameResolverAdapter{repo: repo}
	got, err := r.ResolveAgentRoutingName(t.Context(), "MyPlatformAgent")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := AgentInstanceName(agent)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "agent_") {
		t.Fatalf("expected agent_<uuid> slug, got %q", got)
	}
}

func TestAgentNameResolverAdapter_NotFoundFallsBack(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewAgentRepo(db)
	r := &agentNameResolverAdapter{repo: repo}
	got, err := r.ResolveAgentRoutingName(t.Context(), "NoSuchAgent")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "NoSuchAgent" {
		t.Fatalf("got %q, want verbatim fallback", got)
	}
}

func TestAgentNameResolverAdapter_DBErrorPropagates(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewAgentRepo(db)
	r := &agentNameResolverAdapter{repo: repo}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := r.ResolveAgentRoutingName(ctx, "AnyNonBuiltinAgent")
	if err == nil {
		t.Fatalf("expected error from cancelled ctx, got nil (resolved %q)", got)
	}
	if got != "" {
		t.Fatalf("expected empty string on error, got %q", got)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected wrapped context.Canceled, got %v", err)
	}
	if !strings.Contains(err.Error(), "resolve agent routing name") {
		t.Fatalf("expected wrapped context message, got %v", err)
	}
}
