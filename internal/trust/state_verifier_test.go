// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newTestStateVerifier(t *testing.T, km *KeyManager, componentID string, knownHosts map[string]bool) *StateVerifier {
	t.Helper()
	registry := NewRegistry()
	registry.AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   componentID,
		Namespace:     "test-ns",
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	v := NewVerifier(registry, 5*time.Second, slog.Default())
	return &StateVerifier{
		v:          v,
		seenJTI:    NewInMemoryJTICache(64),
		knownHosts: func() map[string]bool { return knownHosts },
	}
}

func mintRoundTripState(t *testing.T, signer *Signer, claims StateClaims) string {
	t.Helper()
	ss := NewStateSigner(signer)
	raw, err := ss.Mint(claims)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return raw
}

func TestStateVerifier_AcceptsRoundTrip(t *testing.T) {
	km, err := NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)

	claims := StateClaims{
		TargetLinkURI:      "https://example.com/gw/mcp-a/oauth/finish",
		GwPKCEVerifierHash: "abc123",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mcp-a",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			ID:        "jti-1",
		},
	}
	raw := mintRoundTripState(t, signer, claims)

	sv := newTestStateVerifier(t, km, "mcp-a", map[string]bool{"example.com": true})
	got, err := sv.VerifyStateToken(t.Context(), raw)
	if err != nil {
		t.Fatalf("VerifyStateToken: %v", err)
	}
	if got.Issuer != "mcp-a" {
		t.Fatalf("Issuer: want mcp-a, got %s", got.Issuer)
	}
	if got.GwPKCEVerifierHash != "abc123" {
		t.Fatalf("GwPKCEVerifierHash: want abc123, got %s", got.GwPKCEVerifierHash)
	}
}

func TestStateVerifier_RejectsExpiredWithinClockSkewLeeway(t *testing.T) {
	km, err := NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)

	raw := mintRoundTripState(t, signer, StateClaims{
		TargetLinkURI: "https://example.com/gw/mcp-a/oauth/finish",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mcp-a",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Second)),
			ID:        "jti-just-expired",
		},
	})
	sv := newTestStateVerifier(t, km, "mcp-a", map[string]bool{"example.com": true})
	if _, err := sv.VerifyStateToken(t.Context(), raw); err == nil {
		t.Fatal("expected rejection for token expired within clock-skew leeway")
	}
}

func TestStateVerifier_RejectsTamperedPayload(t *testing.T) {
	km, err := NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)
	raw := mintRoundTripState(t, signer, StateClaims{
		TargetLinkURI: "https://example.com/gw/mcp-a/oauth/finish",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mcp-a",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
			ID:        "jti-tamper",
		},
	})
	bytes := []byte(raw)
	firstDot := -1
	for i, b := range bytes {
		if b == '.' {
			firstDot = i
			break
		}
	}
	bytes[firstDot+5] ^= 0x01
	sv := newTestStateVerifier(t, km, "mcp-a", map[string]bool{"example.com": true})
	if _, err := sv.VerifyStateToken(t.Context(), string(bytes)); err == nil {
		t.Fatal("expected rejection for tampered payload")
	}
}

func TestStateVerifier_RejectsExpired(t *testing.T) {
	km, err := NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)
	raw := mintRoundTripState(t, signer, StateClaims{
		TargetLinkURI: "https://example.com/gw/mcp-a/oauth/finish",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mcp-a",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			ID:        "jti-expired",
		},
	})
	sv := newTestStateVerifier(t, km, "mcp-a", map[string]bool{"example.com": true})
	if _, err := sv.VerifyStateToken(t.Context(), raw); err == nil {
		t.Fatal("expected rejection for expired state JWT")
	}
}

func TestStateVerifier_RejectsJTIReplay(t *testing.T) {
	km, err := NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)
	raw := mintRoundTripState(t, signer, StateClaims{
		TargetLinkURI: "https://example.com/gw/mcp-a/oauth/finish",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mcp-a",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			ID:        "jti-replay",
		},
	})
	sv := newTestStateVerifier(t, km, "mcp-a", map[string]bool{"example.com": true})
	if _, err := sv.VerifyStateToken(t.Context(), raw); err != nil {
		t.Fatalf("first verify must pass: %v", err)
	}
	if _, err := sv.VerifyStateToken(t.Context(), raw); err == nil {
		t.Fatal("second verify must reject (replay)")
	}
}

func TestStateVerifier_RejectsUnknownHost(t *testing.T) {
	km, err := NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)
	raw := mintRoundTripState(t, signer, StateClaims{
		TargetLinkURI: "https://evil.example.org/gw/mcp-a/oauth/finish",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mcp-a",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			ID:        "jti-evil",
		},
	})
	sv := newTestStateVerifier(t, km, "mcp-a", map[string]bool{"trusted.example.com": true})
	if _, err := sv.VerifyStateToken(t.Context(), raw); err == nil {
		t.Fatal("expected rejection for target_link_uri host not in allowlist")
	}
}

func TestStateVerifier_RejectsWrongTyp(t *testing.T) {
	km, err := NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)

	claims := StateClaims{
		TargetLinkURI: "https://example.com/gw/mcp-a/oauth/finish",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mcp-a",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
			ID:        "jti-typ",
		},
	}
	raw, err := signer.SignTyped("", claims)
	if err != nil {
		t.Fatalf("SignTyped: %v", err)
	}
	sv := newTestStateVerifier(t, km, "mcp-a", map[string]bool{"example.com": true})
	if _, err := sv.VerifyStateToken(t.Context(), raw); err == nil {
		t.Fatal("expected rejection for missing typ")
	}
}

func TestStateVerifier_SafeUnderRace(t *testing.T) {
	km, err := NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)
	sv := newTestStateVerifier(t, km, "mcp-a", map[string]bool{"example.com": true})

	mint := func(id string) string {
		return mintRoundTripState(t, signer, StateClaims{
			TargetLinkURI: "https://example.com/gw/mcp-a/oauth/finish",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "mcp-a",
				IssuedAt:  jwt.NewNumericDate(time.Now()),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
				ID:        id,
			},
		})
	}
	var wg sync.WaitGroup
	for i := range 100 {
		raw := mint(fmt.Sprintf("jti-race-%d", i))
		wg.Go(func() {
			if _, err := sv.VerifyStateToken(t.Context(), raw); err != nil {
				t.Errorf("verify: %v", err)
			}
		})
	}
	wg.Wait()
}
