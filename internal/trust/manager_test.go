// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type publishFailingBroker struct {
	broker.Broker
	failPublish atomic.Bool
}

func (p *publishFailingBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	if p.failPublish.Load() {
		return errors.New("simulated publish failure")
	}
	return p.Broker.Publish(ctx, topic, msg)
}

func setupBroker(t *testing.T) broker.Broker {
	t.Helper()
	b := broker.NewDevBroker()
	if err := b.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Disconnect(context.Background()) })
	return b
}

func TestManagerInitGateway(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	m := NewManager(cfg, "gateway", "gw-1", "test-ns", b, slog.Default())

	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if m.signer == nil {
		t.Error("gateway should have a signer")
	}
	if m.verifier == nil {
		t.Error("should have a verifier")
	}
}

func TestManagerStartDisabled(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	m := NewManager(cfg, "agent", "agent-1", "test-ns", b, slog.Default())

	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(ctx) }()

	if m.trustQueue != nil {
		t.Error("trust queue should be nil when disabled")
	}

	if m.Registry().CountByType("gateway") != 0 {
		t.Error("no gateway cards expected when disabled")
	}
}

func TestManagerInitAgent(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	m := NewManager(cfg, "agent", "agent-1", "test-ns", b, slog.Default())

	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if m.signer != nil {
		t.Error("agent should NOT have a signer")
	}
	if m.verifier == nil {
		t.Error("should have a verifier")
	}
}

func TestManagerPublishAndReceiveTrustCard(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	gwMgr := NewManager(cfg, "gateway", "gw-1", "test-ns", b, slog.Default())
	if err := gwMgr.Init(); err != nil {
		t.Fatal(err)
	}

	agentMgr := NewManager(cfg, "agent", "agent-1", "test-ns", b, slog.Default())
	if err := agentMgr.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := agentMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentMgr.Stop(ctx) }()

	if err := gwMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gwMgr.Stop(ctx) }()

	deadline := time.After(5 * time.Second)
	for agentMgr.Registry().CountByType("gateway") == 0 {
		select {
		case <-deadline:
			t.Fatal("agent did not receive gateway trust card within timeout")
		case <-time.After(100 * time.Millisecond):
		}
	}

	card := agentMgr.Registry().GetTrustCard("gw-1")
	if card == nil {
		t.Fatal("expected trust card for gw-1")
	}
	if card.ComponentType != "gateway" {
		t.Errorf("ComponentType = %q, want gateway", card.ComponentType)
	}
}

func TestManagerEndToEndJWT(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	gwMgr := NewManager(cfg, "gateway", "gw-1", "test-ns", b, slog.Default())
	if err := gwMgr.Init(); err != nil {
		t.Fatal(err)
	}

	agentMgr := NewManager(cfg, "agent", "agent-1", "test-ns", b, slog.Default())
	if err := agentMgr.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := agentMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentMgr.Stop(ctx) }()

	if err := gwMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gwMgr.Stop(ctx) }()

	deadline := time.After(5 * time.Second)
	for agentMgr.Registry().CountByType("gateway") == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for trust card")
		case <-time.After(100 * time.Millisecond):
		}
	}

	token, err := gwMgr.Signer().Sign(SignClaims{
		Subject: "alice",
		TaskID:  "task-42",
		Roles:   []string{"editor"},
		Scopes:  []string{"tool:*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	identity, err := agentMgr.Authenticator().Authenticate(ctx, token, "task-42")
	if err != nil {
		t.Fatalf("authentication failed: %v", err)
	}
	if identity.UserID != "alice" {
		t.Errorf("UserID = %q, want alice", identity.UserID)
	}
	if len(identity.Scopes) != 1 || identity.Scopes[0] != "tool:*" {
		t.Errorf("Scopes = %v, want [tool:*]", identity.Scopes)
	}
}

func TestManagerRegisterAdditionalSigner_EndToEndJWT(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 1

	hostMgr := NewManager(cfg, "gateway", "host-gw", "test-ns", b, slog.Default())
	if err := hostMgr.Init(); err != nil {
		t.Fatal(err)
	}

	perGwKM, err := NewKeyManager("webui-gw")
	if err != nil {
		t.Fatal(err)
	}
	perGwSigner := NewSigner(perGwKM, "webui-gw", time.Hour, 2*time.Hour)

	subMgr := NewManager(cfg, "agent", "platform", "test-ns", b, slog.Default())
	if err = subMgr.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err = subMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = subMgr.Stop(ctx) }()

	if err = hostMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hostMgr.Stop(ctx) }()

	if err = hostMgr.RegisterAdditionalSigner(ctx, "webui-gw", perGwKM, ""); err != nil {
		t.Fatalf("RegisterAdditionalSigner: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for subMgr.Registry().GetTrustCard("webui-gw") == nil {
		select {
		case <-deadline:
			t.Fatal("subscriber did not receive per-gateway trust card within timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}

	token, err := perGwSigner.Sign(SignClaims{
		Subject: "alice",
		TaskID:  "task-1",
		Roles:   []string{"editor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := subMgr.Authenticator().Authenticate(ctx, token, "task-1")
	if err != nil {
		t.Fatalf("authenticate per-gateway token: %v", err)
	}
	if identity.UserID != "alice" {
		t.Errorf("UserID = %q, want alice", identity.UserID)
	}

	if subMgr.Registry().GetTrustCard("host-gw") == nil {
		t.Error("subscriber lost the host's primary trust card")
	}
}

func TestManagerRegisterAdditionalSigner_LocalSelfVerify(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()

	mgr := NewManager(cfg, "gateway", "host-gw", "test-ns", b, slog.Default())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}

	perGwKM, err := NewKeyManager("webui-gw")
	if err != nil {
		t.Fatal(err)
	}
	perGwSigner := NewSigner(perGwKM, "webui-gw", time.Hour, 2*time.Hour)

	if err = mgr.RegisterAdditionalSigner(t.Context(), "webui-gw", perGwKM, ""); err != nil {
		t.Fatalf("RegisterAdditionalSigner: %v", err)
	}

	if mgr.Registry().GetTrustCard("webui-gw") == nil {
		t.Fatal("local registry should hold the per-gateway card even when pub/sub is disabled")
	}

	token, err := perGwSigner.Sign(SignClaims{
		Subject: "bob",
		TaskID:  "task-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := mgr.Authenticator().Authenticate(t.Context(), token, "task-2")
	if err != nil {
		t.Fatalf("authenticate per-gateway token: %v", err)
	}
	if identity.UserID != "bob" {
		t.Errorf("UserID = %q, want bob", identity.UserID)
	}
}

func TestManagerRegisterAdditionalSigner_MergeOnIDCollision(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 1

	hostMgr := NewManager(cfg, "gateway", "webui-gw", "test-ns", b, slog.Default())
	if err := hostMgr.Init(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Millisecond)

	perGwKM, err := NewKeyManager("webui-gw")
	if err != nil {
		t.Fatal(err)
	}
	perGwSigner := NewSigner(perGwKM, "webui-gw", time.Hour, 2*time.Hour)

	subMgr := NewManager(cfg, "agent", "platform", "test-ns", b, slog.Default())
	if err = subMgr.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err = subMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = subMgr.Stop(ctx) }()

	if err = hostMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hostMgr.Stop(ctx) }()

	if err = hostMgr.RegisterAdditionalSigner(ctx, "webui-gw", perGwKM, ""); err != nil {
		t.Fatalf("RegisterAdditionalSigner: %v", err)
	}

	hostKID := hostMgr.Signer().keyManager.CurrentKeyPair().KeyID
	perGwKID := perGwKM.CurrentKeyPair().KeyID

	deadline := time.After(5 * time.Second)
	for !jwksHasKID(subMgr, "webui-gw", hostKID) || !jwksHasKID(subMgr, "webui-gw", perGwKID) {
		select {
		case <-deadline:
			t.Fatalf("subscriber did not receive merged card with both kids in time (host kid=%s, per-gw kid=%s)",
				hostKID, perGwKID)
		case <-time.After(50 * time.Millisecond):
		}
	}

	hostToken, err := hostMgr.Signer().Sign(SignClaims{
		Subject: "alice",
		TaskID:  "task-host",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = subMgr.Authenticator().Authenticate(ctx, hostToken, "task-host"); err != nil {
		t.Errorf("verify host-signer token (regression: this is the kid-not-found case): %v", err)
	}

	perGwToken, err := perGwSigner.Sign(SignClaims{
		Subject: "alice",
		TaskID:  "task-pergw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subMgr.Authenticator().Authenticate(ctx, perGwToken, "task-pergw"); err != nil {
		t.Errorf("verify per-gateway-signer token: %v", err)
	}
}

func TestManagerRegisterAdditionalSigner_Validation(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	mgr := NewManager(cfg, "gateway", "host-gw", "test-ns", b, slog.Default())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}

	if err := mgr.RegisterAdditionalSigner(t.Context(), "", nil, ""); err == nil {
		t.Error("expected error for empty gatewayID")
	}

	km, err := NewKeyManager("x")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.RegisterAdditionalSigner(t.Context(), "", km, ""); err == nil {
		t.Error("expected error for empty gatewayID")
	}
	if err := mgr.RegisterAdditionalSigner(t.Context(), "gw-x", nil, ""); err == nil {
		t.Error("expected error for nil key manager")
	}

	if mgr.Registry().CountByType("gateway") != 0 {
		t.Errorf("registry should be empty after validation failures, got %d cards",
			mgr.Registry().CountByType("gateway"))
	}
}

func TestManagerRegisterAdditionalSigner_PublishFailureSurfaces(t *testing.T) {
	t.Parallel()
	devBroker := setupBroker(t)
	wrapper := &publishFailingBroker{Broker: devBroker}
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0

	mgr := NewManager(cfg, "gateway", "host-gw", "test-ns", wrapper, slog.Default())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Stop(ctx) }()

	perGwKM, err := NewKeyManager("webui-gw")
	if err != nil {
		t.Fatal(err)
	}

	wrapper.failPublish.Store(true)

	err = mgr.RegisterAdditionalSigner(ctx, "webui-gw", perGwKM, "")
	if err == nil {
		t.Fatal("RegisterAdditionalSigner returned nil despite publish failure — caller lost the ability to fail fast at startup")
	}
	if !strings.Contains(err.Error(), "publish trust card after RegisterAdditionalSigner") {
		t.Errorf("error missing wrap context: %v", err)
	}
	if !strings.Contains(err.Error(), "webui-gw") {
		t.Errorf("error missing gateway_id: %v", err)
	}
}

func TestMergeJWKS_DeduplicatesByKid(t *testing.T) {
	t.Parallel()
	a := map[string]any{"keys": []any{
		map[string]any{"kid": "k1", "kty": "EC", "use": "sig"},
		map[string]any{"kid": "k2", "kty": "EC", "use": "sig"},
	}}
	b := map[string]any{"keys": []any{
		map[string]any{"kid": "k2", "kty": "EC", "use": "sig"},
		map[string]any{"kid": "k3", "kty": "EC", "use": "sig"},
	}}

	got := mergeJWKS(a, b)
	keys, _ := got["keys"].([]any)
	if len(keys) != 3 {
		t.Fatalf("merged keys: got %d entries, want 3 (k1, k2, k3)", len(keys))
	}
	seen := map[string]int{}
	for _, k := range keys {
		seen[jwkKid(k)]++
	}
	for kid, count := range seen {
		if count != 1 {
			t.Errorf("kid %q appeared %d times; want 1", kid, count)
		}
	}
	for _, want := range []string{"k1", "k2", "k3"} {
		if seen[want] != 1 {
			t.Errorf("expected kid %q in merged set", want)
		}
	}
}

func TestMergeJWKS_StableDeduplicationAcrossMultiplePublishes(t *testing.T) {
	t.Parallel()
	jwks1 := map[string]any{"keys": []any{
		map[string]any{"kid": "k1"},
		map[string]any{"kid": "k2"},
	}}
	jwks2 := map[string]any{"keys": []any{
		map[string]any{"kid": "k2"},
		map[string]any{"kid": "k3"},
	}}

	merged := mergeJWKS(jwks1, jwks2)
	for range 5 {
		merged = mergeJWKS(merged, jwks2)
	}
	keys, _ := merged["keys"].([]any)
	if len(keys) != 3 {
		t.Errorf("repeated re-merge bloated the JWKS to %d entries; want 3", len(keys))
	}
}

func TestManagerStartupGatePass(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 1
	cfg.StartupWaitTimeoutSeconds = 10
	cfg.FailOnTimeout = true
	cfg.CardPublishIntervalSeconds = 1

	gwCfg := cfg
	gwCfg.MinRequiredGateways = 0
	gwMgr := NewManager(gwCfg, "gateway", "gw-1", "test-ns", b, slog.Default())
	if err := gwMgr.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := gwMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gwMgr.Stop(ctx) }()

	agentMgr := NewManager(cfg, "agent", "agent-1", "test-ns", b, slog.Default())
	if err := agentMgr.Init(); err != nil {
		t.Fatal(err)
	}
	if err := agentMgr.Start(ctx); err != nil {
		t.Fatalf("startup gate should have passed: %v", err)
	}
	_ = agentMgr.Stop(ctx)
}

func TestManagerStartupGateTimeout(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 1
	cfg.StartupWaitTimeoutSeconds = 1
	cfg.FailOnTimeout = true
	cfg.CardPublishIntervalSeconds = 3600

	m := NewManager(cfg, "agent", "agent-1", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	err := m.Start(context.Background())
	if err == nil {
		defer func() { _ = m.Stop(context.Background()) }()
		t.Fatal("expected startup gate timeout error")
	}
}

func TestManagerStartupGatePermissive(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 1
	cfg.StartupWaitTimeoutSeconds = 1
	cfg.FailOnTimeout = false
	cfg.CardPublishIntervalSeconds = 3600

	m := NewManager(cfg, "agent", "agent-1", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("permissive mode should not fail: %v", err)
	}
	_ = m.Stop(context.Background())
}

func TestManagerSelfRegistersOwnTrustCard(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	gwMgr := NewManager(cfg, "gateway", "gw-self", "test-ns", b, slog.Default())
	if err := gwMgr.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gwMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gwMgr.Stop(ctx) }()

	card := gwMgr.Registry().GetTrustCard("gw-self")
	if card == nil {
		t.Fatal("gateway's own trust card should be in registry immediately after Start()")
	}
	if card.ComponentType != "gateway" {
		t.Errorf("ComponentType = %q, want gateway", card.ComponentType)
	}
	if card.ComponentID != "gw-self" {
		t.Errorf("ComponentID = %q, want gw-self", card.ComponentID)
	}
	if card.Namespace != "test-ns" {
		t.Errorf("Namespace = %q, want test-ns", card.Namespace)
	}
}

func TestManagerInit_RejectsEmptyComponentID(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true

	cases := []struct {
		name          string
		componentType string
		componentID   string
		wantErr       bool
	}{
		{name: "empty_component_id_gateway", componentType: "gateway", componentID: "", wantErr: true},
		{name: "empty_component_id_agent", componentType: "agent", componentID: "", wantErr: true},
		{name: "empty_component_type", componentType: "", componentID: "agent-1", wantErr: true},
		{name: "both_empty", componentType: "", componentID: "", wantErr: true},
		{name: "valid", componentType: "agent", componentID: "agent-1", wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(cfg, tc.componentType, tc.componentID, "test-ns", b, slog.Default())
			err := m.Init()
			if tc.wantErr && err == nil {
				t.Fatalf("Init() with componentType=%q componentID=%q: want error, got nil",
					tc.componentType, tc.componentID)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Init() with componentType=%q componentID=%q: unexpected error: %v",
					tc.componentType, tc.componentID, err)
			}
		})
	}
}

func TestManager_NeverPublishesEmptyComponentTopic(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	q, err := b.CreateQueue("observer", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Subscribe(q, a2a.TrustCardSubscription("test-ns")); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	badTopics := make(chan string, 4)
	go func() {
		for {
			msg, rerr := b.Receive(ctx, q)
			if rerr != nil {
				return
			}
			if msg == nil {
				continue
			}
			if len(msg.Topic) > 0 && (msg.Topic[len(msg.Topic)-1] == '/' ||
				containsEmptySegment(msg.Topic)) {
				badTopics <- msg.Topic
			}
			msg.Ack()
		}
	}()

	m := NewManager(cfg, "gateway", "gw-valid", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(ctx) }()

	time.Sleep(200 * time.Millisecond)

	select {
	case bad := <-badTopics:
		t.Fatalf("trust manager published an empty-segment topic: %q", bad)
	default:
	}
}

func TestManagerTrustCardFanOutAcrossSharedComponentID(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgrA := NewManager(cfg, "str", "shared-worker", "test-ns", b, slog.Default())
	if err := mgrA.Init(); err != nil {
		t.Fatal(err)
	}
	if err := mgrA.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgrA.Stop(ctx) }()

	mgrB := NewManager(cfg, "str", "shared-worker", "test-ns", b, slog.Default())
	if err := mgrB.Init(); err != nil {
		t.Fatal(err)
	}
	if err := mgrB.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgrB.Stop(ctx) }()

	const issuerID = "gw-fanout"
	card := TrustCard{
		ComponentType: "gateway",
		ComponentID:   issuerID,
		Namespace:     "test-ns",
		JWKS:          map[string]any{"keys": []any{}},
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
		Version:       "1.0",
	}
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	topic := a2a.TrustCardTopic("test-ns", "gateway", issuerID)
	if err := b.Publish(ctx, topic, &broker.Message{Payload: data}); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(5 * time.Second)
	for {
		gotA := mgrA.Registry().GetTrustCard(issuerID) != nil
		gotB := mgrB.Registry().GetTrustCard(issuerID) != nil
		if gotA && gotB {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("trust card fan-out failed: mgrA received=%v, mgrB received=%v "+
				"(both must be true for fan-out across replicas with shared componentID)",
				gotA, gotB)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func makeTestCard(id string, expiresAt time.Time) *TrustCard {
	return &TrustCard{
		ComponentType: "gateway",
		ComponentID:   id,
		Namespace:     "test-ns",
		JWKS:          map[string]any{"keys": []any{}},
		IssuedAt:      time.Now().Add(-2 * time.Hour).UnixMilli(),
		ExpiresAt:     expiresAt.UnixMilli(),
		Version:       "1.0",
	}
}

func TestManagerPeriodicExpiredCleanup(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	m := NewManager(cfg, "agent", "agent-cleanup", "test-ns", b, logger)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	m.Registry().AddOrUpdate(makeTestCard("gw-expired", now.Add(-time.Hour)))
	m.Registry().AddOrUpdate(makeTestCard("gw-fresh", now.Add(time.Hour)))

	m.runExpiredCleanup()

	if got := m.Registry().GetTrustCard("gw-expired"); got != nil {
		t.Errorf("expired card should have been removed, got %+v", got)
	}
	if got := m.Registry().GetTrustCard("gw-fresh"); got == nil {
		t.Error("fresh card should still be present")
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "trust registry expired-card cleanup") {
		t.Errorf("expected cleanup log line, got: %q", logs)
	}
	if !strings.Contains(logs, "cardsRemoved=1") {
		t.Errorf("expected cardsRemoved=1 in log, got: %q", logs)
	}

	logBuf.Reset()
	m.runExpiredCleanup()
	if logs := logBuf.String(); logs != "" {
		t.Errorf("expected no log on no-op sweep, got: %q", logs)
	}
}

func TestManagerInit_RejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.CardPublishIntervalSeconds = 0

	m := NewManager(cfg, "agent", "agent-bad-cfg", "test-ns", b, slog.Default())
	err := m.Init()
	if err == nil {
		t.Fatal("Init() with invalid config must return an error")
	}
	if !strings.Contains(err.Error(), "trust config") {
		t.Errorf("error should mention 'trust config', got: %v", err)
	}
}

func TestManagerWaitForGateways_GatewayNoOp(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 1
	cfg.StartupWaitTimeoutSeconds = 10
	cfg.FailOnTimeout = true

	m := NewManager(cfg, "gateway", "gw-noop", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.WaitForGateways(ctx); err != nil {
		t.Fatalf("WaitForGateways on a gateway should be a no-op, got: %v", err)
	}
}

func TestManagerRunExpiredCleanupLoop_FiresOnTick(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true

	m := NewManager(cfg, "agent", "agent-tick", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	m.Registry().AddOrUpdate(makeTestCard("gw-stale", time.Now().Add(-time.Hour)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		m.runExpiredCleanupLoop(ctx, time.Millisecond)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for m.Registry().GetTrustCard("gw-stale") != nil {
		select {
		case <-deadline:
			t.Fatal("ticker did not invoke runExpiredCleanup within 2s")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop did not return after cancel")
	}
}

func TestManagerPeriodicExpiredCleanup_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true

	m := NewManager(cfg, "agent", "agent-cancel", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.periodicExpiredCleanup(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("periodicExpiredCleanup did not return after context cancellation")
	}
}

func TestManagerStartSubscriber_OwnCardInRegistryBeforeReturn(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	m := NewManager(cfg, "gateway", "gw-subscriber", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.StartSubscriber(ctx); err != nil {
		t.Fatalf("StartSubscriber: %v", err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	if got := m.Registry().GetTrustCard("gw-subscriber"); got == nil {
		t.Fatal("expected own trust card in registry after StartSubscriber")
	}
}

func TestManager_NewStateVerifier_HostsFromGatewayCards(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	m := NewManager(cfg, "gateway", "gw-1", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	m.Registry().AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "mcp-a",
		Namespace:     "test-ns",
		PublicURL:     "https://mcp-a.example.com",
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	m.Registry().AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "mcp-no-url",
		Namespace:     "test-ns",
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
		Version:       "1.0",
	})

	sv := m.NewStateVerifier()
	if sv == nil {
		t.Fatal("NewStateVerifier returned nil")
	}
	hosts := sv.knownHosts()
	if !hosts["mcp-a.example.com"] {
		t.Fatalf("expected mcp-a.example.com in knownHosts, got %v", hosts)
	}
	for h := range hosts {
		if h == "" {
			t.Fatalf("empty host should not be present, got %v", hosts)
		}
	}
}

func TestManager_NewStateVerifier_ExpiredCardHostIsExcluded(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	m := NewManager(cfg, "gateway", "gw-1", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	m.Registry().AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "mcp-live",
		Namespace:     "test-ns",
		PublicURL:     "https://mcp-live.example.com",
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	m.Registry().AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "mcp-expired",
		Namespace:     "test-ns",
		PublicURL:     "https://mcp-expired.example.com",
		IssuedAt:      time.Now().Add(-2 * time.Hour).UnixMilli(),
		ExpiresAt:     time.Now().Add(-time.Hour).UnixMilli(),
		Version:       "1.0",
	})

	sv := m.NewStateVerifier()
	hosts := sv.knownHosts()
	if !hosts["mcp-live.example.com"] {
		t.Fatalf("expected live host in knownHosts, got %v", hosts)
	}
	if hosts["mcp-expired.example.com"] {
		t.Fatalf("expired card's host must NOT be in knownHosts, got %v", hosts)
	}
}

func containsEmptySegment(s string) bool {
	for i := 1; i < len(s); i++ {
		if s[i] == '/' && s[i-1] == '/' {
			return true
		}
	}
	return false
}

func TestManagerRejectsMismatchedTrustCard(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	m := NewManager(cfg, "agent", "agent-1", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(ctx) }()

	forgedCard := TrustCard{
		ComponentType: "gateway",
		ComponentID:   "forged-id",
		Namespace:     "test-ns",
		JWKS:          map[string]any{"keys": []any{}},
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
		Version:       "1.0",
	}
	data, _ := json.Marshal(forgedCard)
	topic := a2a.TrustCardTopic("test-ns", "gateway", "legitimate-id")
	_ = b.Publish(ctx, topic, &broker.Message{Payload: data})

	time.Sleep(500 * time.Millisecond)

	if m.Registry().GetTrustCard("forged-id") != nil {
		t.Error("forged trust card should not be in registry")
	}
	if m.Registry().GetTrustCard("legitimate-id") != nil {
		t.Error("mismatched trust card should not be stored under topic-derived ID either")
	}
}

func TestRegisterAdditionalSigner_StampsPublicURL(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	mgr := NewManager(cfg, "gateway", "host-gw", "test-ns", b, slog.Default())
	if err := mgr.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	perGwKM, err := NewKeyManager("gw-mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	const wantURL = "https://gw-mcp-a.example.com/gw/mcp-a/"
	if err := mgr.RegisterAdditionalSigner(t.Context(), "gw-mcp-a", perGwKM, wantURL); err != nil {
		t.Fatalf("RegisterAdditionalSigner: %v", err)
	}
	cards := mgr.Registry().ByType("gateway")
	var found *TrustCard
	for i := range cards {
		if cards[i].ComponentID == "gw-mcp-a" {
			found = cards[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("trust card for gw-mcp-a not found after RegisterAdditionalSigner")
	}
	if found.PublicURL != wantURL {
		t.Errorf("PublicURL = %q, want %q", found.PublicURL, wantURL)
	}
}

func TestSetPrimaryPublicURL_UpdatesAndRepublishes(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	mgr := NewManager(cfg, "gateway", "host-gw", "test-ns", b, slog.Default())
	if err := mgr.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	const wantURL = "https://webui.example.com"
	if err := mgr.SetPrimaryPublicURL(t.Context(), wantURL); err != nil {
		t.Fatalf("SetPrimaryPublicURL: %v", err)
	}
	primary := mgr.Registry().GetTrustCard(mgr.ComponentID())
	if primary == nil {
		t.Fatalf("primary card not found")
	}
	if primary.PublicURL != wantURL {
		t.Errorf("primary card PublicURL = %q, want %q", primary.PublicURL, wantURL)
	}
}

func TestNewStateVerifier_KnownHostsReflectsPublishedPublicURL(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	gwMgr := NewManager(cfg, "gateway", "gw-host", "test-ns", b, slog.Default())
	if err := gwMgr.Init(); err != nil {
		t.Fatal(err)
	}
	peerMgr := NewManager(cfg, "agent", "peer", "test-ns", b, slog.Default())
	if err := peerMgr.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if err := peerMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peerMgr.Stop(ctx) }()
	if err := gwMgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gwMgr.Stop(ctx) }()

	perGwKM, err := NewKeyManager("gw-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	const publicURL = "https://gw-a.example.com/"
	if err := gwMgr.RegisterAdditionalSigner(ctx, "gw-a", perGwKM, publicURL); err != nil {
		t.Fatalf("RegisterAdditionalSigner: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sv := peerMgr.NewStateVerifier()
		hosts := sv.KnownHosts()
		if hosts["gw-a.example.com"] {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("knownHosts did not pick up gw-a.example.com within 2s")
}

func TestManagerInit_KeyPersistenceDir_StableAcrossRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfgBefore := DefaultConfig()
	cfgBefore.SigningKey.PersistenceDir = dir
	before := NewManager(cfgBefore, "gateway", "gw-1", "test-ns", setupBroker(t), slog.Default())
	if err := before.Init(); err != nil {
		t.Fatal(err)
	}

	cfgAfter := DefaultConfig()
	cfgAfter.SigningKey.PersistenceDir = dir
	after := NewManager(cfgAfter, "gateway", "gw-1", "test-ns", setupBroker(t), slog.Default())
	if err := after.Init(); err != nil {
		t.Fatal(err)
	}

	if !before.Signer().PublicKey().Equal(after.Signer().PublicKey()) {
		t.Error("restarting a gateway with the same KeyPersistenceDir must reuse the same signing key")
	}
	beforeKID := before.Signer().keyManager.CurrentKeyPair().KeyID
	afterKID := after.Signer().keyManager.CurrentKeyPair().KeyID
	if beforeKID != afterKID {
		t.Errorf("kid must be stable across restarts: %q vs %q", beforeKID, afterKID)
	}
}

func TestLogKeyOrigin_EphemeralWarnsOnlyForSigners(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)

	t.Run("gateway warns", func(t *testing.T) {
		t.Parallel()
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		cfg := DefaultConfig()
		m := NewManager(cfg, "gateway", "gw-warn", "test-ns", b, logger)
		if err := m.Init(); err != nil {
			t.Fatal(err)
		}
		logs := logBuf.String()
		if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "trust signing key is ephemeral") {
			t.Errorf("expected a WARN ephemeral-key log for a gateway, got: %q", logs)
		}
	})

	t.Run("agent does not warn", func(t *testing.T) {
		t.Parallel()
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		cfg := DefaultConfig()
		m := NewManager(cfg, "agent", "agent-noop", "test-ns", b, logger)
		if err := m.Init(); err != nil {
			t.Fatal(err)
		}
		logs := logBuf.String()
		if !strings.Contains(logs, "level=INFO") || !strings.Contains(logs, "trust signing key is ephemeral") {
			t.Errorf("expected an INFO ephemeral-key log for an agent, got: %q", logs)
		}
		if strings.Contains(logs, "level=WARN") {
			t.Errorf("agent's ephemeral key should not WARN, got: %q", logs)
		}
	})

	t.Run("platform warns (mints JWTs, no config path to fix it yet)", func(t *testing.T) {
		t.Parallel()
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		cfg := DefaultConfig()
		m := NewManager(cfg, "platform", "platform-noop", "test-ns", b, logger)
		if err := m.Init(); err != nil {
			t.Fatal(err)
		}
		logs := logBuf.String()
		if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "trust signing key is ephemeral") {
			t.Errorf("expected a WARN ephemeral-key log for platform (it mints JWTs too), got: %q", logs)
		}
	})
}

func TestLogKeyOrigin_EphemeralDegradedAlwaysWarns(t *testing.T) {
	t.Parallel()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	km, err := NewKeyManagerWithSeedOrPersistence("../escape", SigningKeyConfig{PersistenceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if km.Origin() != KeyOriginEphemeralDegraded {
		t.Fatalf("Origin() = %v, want KeyOriginEphemeralDegraded (test setup problem, not the thing under test)", km.Origin())
	}

	logKeyOrigin(logger, km, "gw-degraded", "gateway")
	logs := logBuf.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "persistence failed") {
		t.Errorf("expected a WARN persistence-failed log, got: %q", logs)
	}
}

func TestManagerInit_SeedDerived_LogsStableINFO(t *testing.T) {
	t.Parallel()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := DefaultConfig()
	cfg.SigningKey.Seed = []byte("a-stable-session-secret-key-value")
	m := NewManager(cfg, "gateway", "gw-seeded", "test-ns", setupBroker(t), logger)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}

	if got := m.Signer().keyManager.Origin(); got != KeyOriginSeedDerived {
		t.Fatalf("Origin() = %v, want KeyOriginSeedDerived (test setup problem, not the thing under test)", got)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "level=INFO") || !strings.Contains(logs, "seed-derived") {
		t.Errorf("expected an INFO seed-derived log, got: %q", logs)
	}
	if strings.Contains(logs, "level=WARN") {
		t.Errorf("seed-derived key should not WARN, got: %q", logs)
	}
}
