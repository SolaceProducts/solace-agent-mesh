// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestKeyRotationIntegration(t *testing.T) {
	b := setupBroker(t)

	signerCfg := DefaultConfig()
	signerCfg.Enabled = true
	signerCfg.MinRequiredGateways = 0
	signerCfg.CardPublishIntervalSeconds = 60
	signerCfg.JWTDefaultTTLSeconds = 30
	signerCfg.KeyRotationInterval = 0
	signerCfg.KeyOverlapRetention = 250 * time.Millisecond

	verifierCfg := DefaultConfig()
	verifierCfg.Enabled = true
	verifierCfg.MinRequiredGateways = 0
	verifierCfg.CardPublishIntervalSeconds = 60

	logger := slog.Default()

	signer := NewManager(signerCfg, "gateway", "rot-gw", "rot-ns", b, logger)
	if err := signer.Init(); err != nil {
		t.Fatal(err)
	}

	verifier := NewManager(verifierCfg, "agent", "rot-agent", "rot-ns", b, logger)
	if err := verifier.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := verifier.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = verifier.Stop(ctx) }()

	if err := signer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = signer.Stop(ctx) }()

	waitForKID(t, verifier, "rot-gw", signer.keyManager.CurrentKeyPair().KeyID, 2*time.Second)

	kp1 := signer.keyManager.CurrentKeyPair()

	jwtA, err := signer.Signer().Sign(SignClaims{Subject: "alice", TaskID: "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, vErr := verifier.Authenticator().Authenticate(ctx, jwtA, "t-1"); vErr != nil {
		t.Fatalf("pre-rotation verify of JWT_a: %v", vErr)
	}

	if rErr := signer.rotateAndPublish(ctx); rErr != nil {
		t.Fatal(rErr)
	}
	kp2 := signer.keyManager.CurrentKeyPair()
	if kp1.KeyID == kp2.KeyID {
		t.Fatal("rotation did not change kid")
	}

	waitForBothKIDs(t, verifier, "rot-gw", kp1.KeyID, kp2.KeyID, 2*time.Second)

	if _, vErr := verifier.Authenticator().Authenticate(ctx, jwtA, "t-1"); vErr != nil {
		t.Fatalf("overlap-window verify of JWT_a (kid_1): %v", vErr)
	}

	jwtB, err := signer.Signer().Sign(SignClaims{Subject: "bob", TaskID: "t-2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Authenticator().Authenticate(ctx, jwtB, "t-2"); err != nil {
		t.Fatalf("post-rotation verify of JWT_b (kid_2): %v", err)
	}

	time.Sleep(signerCfg.KeyOverlapRetention + 100*time.Millisecond)
	if err := signer.publishTrustCard(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOnlyKID(t, verifier, "rot-gw", kp2.KeyID, 2*time.Second)

	if _, err := verifier.Authenticator().Authenticate(ctx, jwtA, "t-1"); err == nil {
		t.Fatal("expected verify of JWT_a to fail after kid_1 aged out")
	}

	if _, err := verifier.Authenticator().Authenticate(ctx, jwtB, "t-2"); err != nil {
		t.Fatalf("post-prune verify of JWT_b (kid_2): %v", err)
	}
}

func TestKeyRotationPeriodicLoopRotates(t *testing.T) {
	b := setupBroker(t)

	signerCfg := DefaultConfig()
	signerCfg.Enabled = true
	signerCfg.MinRequiredGateways = 0
	signerCfg.CardPublishIntervalSeconds = 60
	signerCfg.KeyRotationInterval = 200 * time.Millisecond
	signerCfg.KeyOverlapRetention = 500 * time.Millisecond

	verifierCfg := DefaultConfig()
	verifierCfg.Enabled = true
	verifierCfg.MinRequiredGateways = 0
	verifierCfg.CardPublishIntervalSeconds = 60

	logger := slog.Default()

	signer := NewManager(signerCfg, "gateway", "loop-gw", "loop-ns", b, logger)
	if err := signer.Init(); err != nil {
		t.Fatal(err)
	}
	verifier := NewManager(verifierCfg, "agent", "loop-agent", "loop-ns", b, logger)
	if err := verifier.Init(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := verifier.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = verifier.Stop(ctx) }()
	if err := signer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = signer.Stop(ctx) }()

	initialKID := signer.keyManager.CurrentKeyPair().KeyID
	waitForKID(t, verifier, "loop-gw", initialKID, 1*time.Second)

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if signer.keyManager.CurrentKeyPair().KeyID != initialKID {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	rotatedKID := signer.keyManager.CurrentKeyPair().KeyID
	if rotatedKID == initialKID {
		t.Fatalf("periodic rotation goroutine never rotated within 1s")
	}

	waitForKID(t, verifier, "loop-gw", rotatedKID, 1*time.Second)
}

func TestKeyRotationDisabledNoGoroutine(t *testing.T) {
	t.Parallel()
	b := setupBroker(t)

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	m := NewManager(cfg, "gateway", "no-rot-gw", "test-ns", b, slog.Default())
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(ctx) }()

	originalKID := m.keyManager.CurrentKeyPair().KeyID
	time.Sleep(100 * time.Millisecond)
	if got := m.keyManager.CurrentKeyPair().KeyID; got != originalKID {
		t.Errorf("kid changed unexpectedly with rotation disabled: %s -> %s", originalKID, got)
	}
}

func waitForKID(t *testing.T, m *Manager, componentID, kid string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if jwksHasKID(m, componentID, kid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("verifier never received kid %s for %s within %v", kid, componentID, timeout)
}

func waitForBothKIDs(t *testing.T, m *Manager, componentID, kid1, kid2 string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if jwksHasKID(m, componentID, kid1) && jwksHasKID(m, componentID, kid2) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("verifier never received both kids (%s, %s) for %s within %v", kid1, kid2, componentID, timeout)
}

func waitForOnlyKID(t *testing.T, m *Manager, componentID, wantKID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		jwks, ok := m.Registry().GetJWKS(componentID)
		if ok {
			keys, _ := jwks["keys"].([]any)
			if len(keys) == 1 {
				if k, _ := keys[0].(map[string]any)["kid"].(string); k == wantKID {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("verifier JWKS for %s never narrowed to single kid %s within %v", componentID, wantKID, timeout)
}

func jwksHasKID(m *Manager, componentID, wantKID string) bool {
	jwks, ok := m.Registry().GetJWKS(componentID)
	if !ok {
		return false
	}
	keys, _ := jwks["keys"].([]any)
	for _, k := range keys {
		entry, _ := k.(map[string]any)
		if entry == nil {
			continue
		}
		if got, _ := entry["kid"].(string); got == wantKID {
			return true
		}
	}
	return false
}
