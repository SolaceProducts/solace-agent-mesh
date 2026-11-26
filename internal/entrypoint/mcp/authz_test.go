// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type authzStubResolver struct{ scopes []string }

func (r authzStubResolver) ResolveScopes(string, []string) []string { return r.scopes }

type stubGatewayAdapter struct{}

func (stubGatewayAdapter) Init(context.Context, *entrypoint.GatewayCore) error { return nil }
func (stubGatewayAdapter) Start(context.Context) error                         { return nil }
func (stubGatewayAdapter) Stop(context.Context) error                          { return nil }
func (stubGatewayAdapter) MountIntent() entrypoint.MountSpec                   { return entrypoint.MountSpec{} }
func (stubGatewayAdapter) OnStatusUpdate(context.Context, string, *a2a.TaskStatusUpdateEvent, []byte) {
}
func (stubGatewayAdapter) OnArtifactUpdate(context.Context, string, *a2a.TaskArtifactUpdateEvent, []byte) {
}
func (stubGatewayAdapter) OnFinalResponse(context.Context, string, *a2a.Task, []byte) {}
func (stubGatewayAdapter) OnError(context.Context, string, *a2a.Error, []byte)        {}
func (stubGatewayAdapter) OnAgentDiscovered(*a2a.AgentCard)                           {}
func (stubGatewayAdapter) OnAgentRemoved(string)                                      {}

func newAuthzTestAdapter(t *testing.T, cfg *Config, extraAppConfig map[string]any) (*Adapter, *broker.DevBroker) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-mcp-gw",
	}
	maps.Copy(appConfig, extraAppConfig)
	core := entrypoint.NewGatewayCore(stubGatewayAdapter{}, appConfig)
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("core start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	return &Adapter{
		core:   core,
		cfg:    cfg,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, brk
}

func TestMCPSubmit_RBACEnforcedOnUnauthenticatedEntrypoint(t *testing.T) {
	const permitted = "PermittedAgent"
	const forbidden = "ForbiddenAgent"

	tests := []struct {
		name                        string
		cfg                         *Config
		wireResolver                bool
		scopes                      []string
		authzEnabledWithoutResolver bool
		target                      string
		wantPublished               bool
		wantPrincipal               string
	}{
		{
			name:          "run_as scoped to the target is allowed",
			cfg:           &Config{RunAs: "scoped"},
			wireResolver:  true,
			scopes:        []string{rbac.AgentInvokeScope(permitted)},
			target:        permitted,
			wantPublished: true,
			wantPrincipal: "system:scoped",
		},
		{
			name:          "run_as NOT scoped to the target is denied",
			cfg:           &Config{RunAs: "scoped"},
			wireResolver:  true,
			scopes:        []string{rbac.AgentInvokeScope(permitted)},
			target:        forbidden,
			wantPublished: false,
			wantPrincipal: "system:scoped",
		},
		{
			name:          "default_user_identity holding no scopes is denied",
			cfg:           &Config{DefaultUserIdentity: "nobody@example.com"},
			wireResolver:  true,
			scopes:        nil,
			target:        permitted,
			wantPublished: false,
			wantPrincipal: "nobody@example.com",
		},
		{
			name:                        "enforcing gateway whose resolver failed denies rather than leaking the wildcard",
			cfg:                         &Config{RunAs: "scoped"},
			authzEnabledWithoutResolver: true,
			target:                      permitted,
			wantPublished:               false,
			wantPrincipal:               "system:scoped",
		},
		{
			name:          "no authz configured at all still allows (community/dev)",
			cfg:           &Config{RunAs: "scoped"},
			target:        forbidden,
			wantPublished: true,
			wantPrincipal: "system:scoped",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, brk := newAuthzTestAdapter(t, tc.cfg, nil)
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: permitted})
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: forbidden})
			if tc.wireResolver {
				a.core.SetScopeResolver(authzStubResolver{scopes: tc.scopes})
			}
			if tc.authzEnabledWithoutResolver {
				a.core.SetAuthzEnabled(true)
			}

			q, err := brk.CreateQueue("mcp-authz-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", tc.target)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			identity := a.defaultIdentity()
			if identity.UserID() != tc.wantPrincipal {
				t.Errorf("principal = %q, want %q", identity.UserID(), tc.wantPrincipal)
			}
			ctx := gwauth.ContextWithSubject(t.Context(), identity)

			msg := a2a.Message{
				Kind:      "message",
				MessageID: idgen.New(),
				Role:      "user",
				Parts:     []a2a.Part{a2a.NewTextPart("invoke the agent")},
				TaskID:    idgen.New(),
				ContextID: idgen.New(),
			}
			submitErr := a.core.SubmitTask(ctx, tc.target, msg.TaskID, msg, identity)

			if tc.wantPublished && submitErr != nil {
				t.Fatalf("SubmitTask returned %v, want success", submitErr)
			}
			if !tc.wantPublished {
				if submitErr == nil {
					t.Fatal("SubmitTask succeeded, want a delegation denial")
				}
				if !errors.Is(submitErr, entrypoint.ErrAgentDelegationDenied) {
					t.Fatalf("SubmitTask error = %v, want ErrAgentDelegationDenied", submitErr)
				}
			}

			timeout := 300 * time.Millisecond
			if tc.wantPublished {
				timeout = 2 * time.Second
			}
			recvCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			received, _ := brk.Receive(recvCtx, q)

			if tc.wantPublished && received == nil {
				t.Fatal("expected the task published to the agent request topic, got none")
			}
			if !tc.wantPublished && received != nil {
				t.Fatalf("expected no publish (RBAC deny), got payload=%s", string(received.Payload))
			}
		})
	}
}

func TestMCPSubmit_CustomRunAsCarriesTheRenderedSystemUserRow(t *testing.T) {
	const qaBotID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42"
	a, brk := newAuthzTestAdapter(t, &Config{RunAs: "qa-bot"},
		map[string]any{"run_as_user_uuids": map[string]any{"system:qa-bot": qaBotID}})
	q, err := brk.CreateQueue("mcp-run-as-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TargetAgent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := a.defaultIdentity()
	msg := a2a.Message{Kind: "message", MessageID: idgen.New(), Role: "user", Parts: []a2a.Part{a2a.NewTextPart("hi")}, TaskID: idgen.New(), ContextID: idgen.New()}
	if err = a.core.SubmitTask(t.Context(), "TargetAgent", msg.TaskID, msg, identity); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	if received == nil {
		t.Fatal("expected the task published to the agent request topic, got none")
	}
	if props := a2a.DecodeUserProperties(received.UserProperties); props.UserID != "system:qa-bot" || props.UserUUID != qaBotID {
		t.Errorf("task ran as %q with user uuid %q, want system:qa-bot with %s", props.UserID, props.UserUUID, qaBotID)
	}
}
