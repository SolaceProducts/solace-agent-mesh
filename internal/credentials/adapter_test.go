// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package credentials

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

var _ auth.CredentialService = (*AuthAdapter)(nil)

func TestAuthAdapter_OAuth2Integration(t *testing.T) {
	ctx := context.Background()
	const (
		agentName = "EntOAuthAgent"
		userA     = "userA"
		userB     = "userB"
		credKey   = "ent_mcp_oauth"
	)

	memStore := session.NewMemoryStore()
	km, err := identity.NewKeyManager(identity.Config{Mode: "manual", Key: "primary-key-1234567890ab"}, agentName, nil, nil)
	if err != nil {
		t.Fatalf("primary KeyManager: %v", err)
	}
	adapter := NewAuthAdapter(NewSessionStore(memStore, km, nil))

	tokenAJSON, _ := json.Marshal(map[string]any{"access_token": "at-A", "refresh_token": "rt-A", "token_type": "Bearer"})
	tokenBJSON, _ := json.Marshal(map[string]any{"access_token": "at-B", "refresh_token": "rt-B", "token_type": "Bearer"})

	if setErr := adapter.Set(ctx, agentName, agentName, userA, credKey, string(tokenAJSON)); setErr != nil {
		t.Fatalf("Set userA: %v", setErr)
	}
	if setErr := adapter.Set(ctx, agentName, agentName, userB, credKey, string(tokenBJSON)); setErr != nil {
		t.Fatalf("Set userB: %v", setErr)
	}

	gotA, err := adapter.Get(ctx, agentName, agentName, userA, credKey)
	if err != nil {
		t.Fatalf("Get userA: %v", err)
	}
	if gotA != string(tokenAJSON) {
		t.Errorf("Get userA = %q, want %q", gotA, string(tokenAJSON))
	}
	gotB, err := adapter.Get(ctx, agentName, agentName, userB, credKey)
	if err != nil {
		t.Fatalf("Get userB: %v", err)
	}
	if gotB != string(tokenBJSON) {
		t.Errorf("Get userB = %q, want %q", gotB, string(tokenBJSON))
	}

	if gotA == gotB {
		t.Error("userA and userB must resolve to distinct credentials")
	}

	appName := secretAppName(agentName)
	sessionID := deriveSessionID(km.IdentityKey(), appName, userA, credKey)
	sess, err := memStore.Get(ctx, appName, userA, sessionID)
	if err != nil {
		t.Fatalf("fetch raw session: %v", err)
	}
	if sess == nil {
		t.Fatal("raw session missing — AuthAdapter.Set did not persist through the store")
	}
	secretVal, ok := sess.Metadata["secret_value"].(string)
	if !ok || secretVal == "" {
		t.Fatal("session metadata has no secret_value field")
	}
	decoded, err := base64.StdEncoding.DecodeString(secretVal)
	if err != nil {
		t.Fatalf("secret_value not valid base64 (expected ciphertext): %v", err)
	}
	var asJSON any
	if json.Unmarshal(decoded, &asJSON) == nil {
		t.Error("decoded secret_value is valid JSON — token was stored in plaintext")
	}

	km2, err := identity.NewKeyManager(identity.Config{Mode: "manual", Key: "different-key-abcdef12345678"}, agentName, nil, nil)
	if err != nil {
		t.Fatalf("secondary KeyManager: %v", err)
	}
	if _, decErr := identity.Decrypt(km2.EncryptionKey(), decoded); decErr == nil {
		t.Error("identity.Decrypt accepted ciphertext under a different encryption key — AES-GCM key binding broken")
	}
}

func TestAuthAdapterSealOpensOnlyUnderTheSameIdentityKey(t *testing.T) {
	newAdapter := func(key string) *AuthAdapter {
		km, err := identity.NewKeyManager(identity.Config{Mode: "manual", Key: key}, "agent", nil, nil)
		if err != nil {
			t.Fatalf("KeyManager: %v", err)
		}
		return NewAuthAdapter(NewSessionStore(session.NewMemoryStore(), km, nil))
	}
	a := newAdapter("primary-key-1234567890ab")

	sealed, err := a.Seal("token")
	if err != nil || sealed == "" || sealed == "token" {
		t.Fatalf("Seal = %q, %v", sealed, err)
	}
	if got, err := newAdapter("primary-key-1234567890ab").Open(sealed); err != nil || got != "token" {
		t.Errorf("Open under the same key = %q, %v", got, err)
	}
	if _, err := newAdapter("different-key-abcdef12345678").Open(sealed); err == nil {
		t.Error("Open under another key succeeded")
	}
	if _, err := a.Open("not base64!"); err == nil {
		t.Error("Open of a malformed value succeeded")
	}

	noKey := NewAuthAdapter(NoopService{})
	if _, err := noKey.Seal("token"); err == nil {
		t.Error("Seal with no identity key succeeded")
	}
	if _, err := noKey.Open(sealed); err == nil {
		t.Error("Open with no identity key succeeded")
	}
}
