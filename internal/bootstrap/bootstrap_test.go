// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/audit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func testBootstrapConfig(t *testing.T) (BootstrapConfig, broker.Broker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := BootstrapConfig{
		TrustConfig:    trust.DefaultConfig(),
		IdentityConfig: identity.DefaultConfig(),
		ComponentType:  "agent",
		ComponentID:    "test-agent-01",
		Namespace:      "test-ns",
		Broker:         brk,
		Logger:         slog.Default(),
	}
	return cfg, brk
}

func TestBootstrapCreatesAllComponents(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)

	ec, opts, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}

	if ec.TrustManager == nil {
		t.Error("TrustManager should not be nil")
	}
	if ec.KeyManager == nil {
		t.Error("KeyManager should not be nil")
	}
	if ec.AuditLogger == nil {
		t.Error("AuditLogger should not be nil")
	}
	if ec.ToolAuth == nil {
		t.Error("ToolAuth should not be nil")
	}
	if ec.AgentAuth == nil {
		t.Error("AgentAuth should not be nil")
	}

	if ec.ControlAuth == nil {
		t.Error("ControlAuth should not be nil")
	}

	if len(opts) != 7 {
		t.Errorf("expected 7 runtime options (no credential service), got %d", len(opts))
	}
}

func TestBootstrapWithSessionStore(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)
	cfg.SessionStore = session.NewMemoryStore()

	ec, opts, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}

	if ec.TrustManager == nil {
		t.Error("TrustManager should not be nil")
	}

	if len(opts) != 8 {
		t.Errorf("expected 8 runtime options (with credential service), got %d", len(opts))
	}
}

func TestBootstrap_KeyPersistenceDir_PersistsKeyFile(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)
	cfg.ComponentType = "gateway"
	cfg.ComponentID = "gw-bootstrap"
	dir := t.TempDir()
	cfg.SigningKey.PersistenceDir = dir

	ec, _, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}

	if ec.TrustManager.Signer() == nil {
		t.Fatal("gateway should have a signer")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "trust_signing_gw-bootstrap.pem" {
		t.Errorf("expected exactly one persisted key file trust_signing_gw-bootstrap.pem, got %v", entries)
	}
}

func TestBootstrapRBACEnforcement(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)

	ec, _, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	identity := &auth.Identity{
		UserID: "test@example.com",
		Scopes: []string{"agent:ResearcherAgent:invoke", "tool:read_file:invoke"},
	}

	if err := ec.AgentAuth.AuthorizeAgent(ctx, identity, "ResearcherAgent"); err != nil {
		t.Errorf("expected agent access allowed, got: %v", err)
	}

	if err := ec.AgentAuth.AuthorizeAgent(ctx, identity, "AdminAgent"); err == nil {
		t.Error("expected agent access denied for AdminAgent")
	}

	if err := ec.ToolAuth.AuthorizeTool(ctx, identity, "read_file", []string{"tool:read_file:invoke"}); err != nil {
		t.Errorf("expected tool access allowed, got: %v", err)
	}

	if err := ec.ToolAuth.AuthorizeTool(ctx, identity, "delete_file", []string{"tool:delete_file:invoke"}); err == nil {
		t.Error("expected tool access denied for delete_file")
	}
}

func TestBootstrapRBACWildcard(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)

	ec, _, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	identity := &auth.Identity{
		UserID: "admin@example.com",
		Scopes: []string{"*"},
	}

	if err := ec.AgentAuth.AuthorizeAgent(ctx, identity, "AnyAgent"); err != nil {
		t.Errorf("wildcard should allow all agents: %v", err)
	}
	if err := ec.ToolAuth.AuthorizeTool(ctx, identity, "any_tool", []string{"tool:any:invoke"}); err != nil {
		t.Errorf("wildcard should allow all tools: %v", err)
	}
}

func TestBootstrapRBACNoScopes(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)

	ec, _, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	identity := &auth.Identity{
		UserID: "nobody@example.com",
		Scopes: []string{},
	}

	if err := ec.AgentAuth.AuthorizeAgent(ctx, identity, "TestAgent"); err == nil {
		t.Error("expected agent access denied for user with no scopes")
	}
	if err := ec.ToolAuth.AuthorizeTool(ctx, identity, "read_file", []string{"tool:read_file:invoke"}); err == nil {
		t.Error("expected tool access denied for user with no scopes")
	}
}

func TestBootstrapToolFiltering(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)

	ec, _, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	identity := &auth.Identity{
		UserID: "analyst@example.com",
		Scopes: []string{"tool:read_file:invoke", "tool:search:*"},
	}

	tools := []auth.ToolInfo{
		{Name: "read_file", RequiredScopes: []string{"tool:read_file:invoke"}},
		{Name: "search_replace", RequiredScopes: []string{"tool:search:invoke"}},
		{Name: "delete_file", RequiredScopes: []string{"tool:delete_file:invoke"}},
		{Name: "no_scopes_tool", RequiredScopes: nil},
	}

	filtered := ec.ToolAuth.FilterTools(ctx, identity, tools)

	names := make(map[string]bool)
	for _, t := range filtered {
		names[t.Name] = true
	}

	if !names["read_file"] {
		t.Error("read_file should be in filtered list")
	}
	if !names["search_replace"] {
		t.Error("search_replace should be in filtered list (tool:search:* matches)")
	}
	if names["delete_file"] {
		t.Error("delete_file should NOT be in filtered list")
	}
	if !names["no_scopes_tool"] {
		t.Error("no_scopes_tool should be in filtered list (no scopes required)")
	}
}

func TestBootstrapAuditLogging(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg.Logger = logger

	ec, _, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := ec.AuditLogger.(auth.NoopAuditLogger); ok {
		t.Error("expected real audit logger, got noop")
	}

	ctx := context.Background()
	identity := &auth.Identity{
		UserID: "test@example.com",
		Scopes: []string{},
	}
	_ = ec.AgentAuth.AuthorizeAgent(ctx, identity, "SomeAgent")

	logOutput := buf.String()
	if logOutput == "" {
		t.Error("expected audit log output, got nothing")
	}
}

func TestBootstrapDisabledAudit(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)

	enabled := false
	cfg.AuditConfig = audit.Config{Enabled: &enabled}

	ec, _, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := ec.AuditLogger.(auth.NoopAuditLogger); !ok {
		t.Error("expected NoopAuditLogger when audit is disabled")
	}
}

func TestBootstrapCredentialService(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)
	cfg.SessionStore = session.NewMemoryStore()

	ec, _, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	key := ec.KeyManager.IdentityKey()
	if key == "" {
		t.Error("expected non-empty identity key")
	}
	if len(key) < 32 {
		t.Errorf("expected identity key length >= 32 hex chars, got %d", len(key))
	}
}

func TestPickAuthenticatorTrustDisabled(t *testing.T) {
	verifier := stubVerifier{}
	got := pickAuthenticator(false, verifier, slog.Default())
	if _, ok := got.(auth.NoopAuthenticator); !ok {
		t.Errorf("trust disabled: expected NoopAuthenticator, got %T", got)
	}
}

func TestPickAuthenticatorTrustEnabled(t *testing.T) {
	verifier := stubVerifier{}
	got := pickAuthenticator(true, verifier, slog.Default())
	if _, ok := got.(stubVerifier); !ok {
		t.Errorf("trust enabled: expected verifier passthrough, got %T", got)
	}
}

type stubVerifier struct{}

func (stubVerifier) Authenticate(_ context.Context, _ string, _ string) (*auth.Identity, error) {
	return nil, nil
}

func TestBootstrapNilLogger(t *testing.T) {
	cfg, _ := testBootstrapConfig(t)
	cfg.Logger = nil

	ec, _, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Bootstrap with nil logger should succeed: %v", err)
	}
	if ec.TrustManager == nil {
		t.Error("TrustManager should not be nil")
	}
}

func TestPickAuthorizersFollowTrust(t *testing.T) {
	auditLog := auth.NoopAuditLogger{}
	logger := slog.Default()
	realTool := rbac.NewToolAuth(auditLog, logger)
	realAgent := rbac.NewAgentAuth(auditLog, logger)
	realControl := rbac.NewControlAuth(auditLog, logger)

	if pickToolAuthorizer(true, realTool) != auth.ToolAuthorizer(realTool) {
		t.Error("trust enabled: tool authorizer should be the real one")
	}
	if pickAgentAuthorizer(true, realAgent) != auth.AgentAuthorizer(realAgent) {
		t.Error("trust enabled: agent authorizer should be the real one")
	}
	if pickControlAuthorizer(true, realControl) != auth.ControlAuthorizer(realControl) {
		t.Error("trust enabled: control authorizer should be the real one")
	}

	if _, ok := pickToolAuthorizer(false, realTool).(auth.NoopToolAuthorizer); !ok {
		t.Errorf("trust disabled: tool authorizer = %T, want NoopToolAuthorizer", pickToolAuthorizer(false, realTool))
	}
	if _, ok := pickAgentAuthorizer(false, realAgent).(auth.NoopAgentAuthorizer); !ok {
		t.Errorf("trust disabled: agent authorizer = %T, want NoopAgentAuthorizer", pickAgentAuthorizer(false, realAgent))
	}
	if _, ok := pickControlAuthorizer(false, realControl).(auth.NoopControlAuthorizer); !ok {
		t.Errorf("trust disabled: control authorizer = %T, want NoopControlAuthorizer", pickControlAuthorizer(false, realControl))
	}
}

func TestBootstrapBypassesAuthorizersWhenTrustDisabled(t *testing.T) {
	cfg, brk := testBootstrapConfig(t)
	if cfg.TrustConfig.Enabled {
		t.Fatal("precondition: test config should have trust disabled")
	}

	_, opts, err := Bootstrap(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}

	svc := runtime.NewRuntime(brk, config.FromMap(nil), opts...).Services()
	if _, ok := svc.AgentAuthorizer().(auth.NoopAgentAuthorizer); !ok {
		t.Errorf("Services().AgentAuthorizer() = %T, want NoopAgentAuthorizer", svc.AgentAuthorizer())
	}
	if _, ok := svc.ToolAuthorizer().(auth.NoopToolAuthorizer); !ok {
		t.Errorf("Services().ToolAuthorizer() = %T, want NoopToolAuthorizer", svc.ToolAuthorizer())
	}
	if _, ok := svc.ControlAuthorizer().(auth.NoopControlAuthorizer); !ok {
		t.Errorf("Services().ControlAuthorizer() = %T, want NoopControlAuthorizer", svc.ControlAuthorizer())
	}

	required := []string{"agent:sam_dev_user____test__abc__def:invoke"}
	if err := svc.AgentAuthorizer().AuthorizeAgentScopes(context.Background(), nil, required); err != nil {
		t.Errorf("auth-off: test-agent required_scopes should be bypassed, got: %v", err)
	}
}
