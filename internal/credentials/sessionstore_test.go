// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package credentials

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
)

func setupStore(t *testing.T) (*SessionStore, *identity.KeyManager, session.Store) {
	t.Helper()
	memStore := session.NewMemoryStore()
	cfg := identity.Config{Mode: "manual", Key: "test-key-1234567890abcdef"}
	km, err := identity.NewKeyManager(cfg, "test-agent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewSessionStore(memStore, km, nil), km, memStore
}

func TestCRUDRoundTrip(t *testing.T) {
	store, _, _ := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	if err := store.Set(ctx, scope, "api_token", "secret-123", 0); err != nil {
		t.Fatal(err)
	}

	val, err := store.Get(ctx, scope, "api_token")
	if err != nil {
		t.Fatal(err)
	}
	if val != "secret-123" {
		t.Errorf("Get = %q, want %q", val, "secret-123")
	}

	keys, err := store.List(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "api_token" {
		t.Errorf("List = %v, want [api_token]", keys)
	}

	if delErr := store.Delete(ctx, scope, "api_token"); delErr != nil {
		t.Fatal(delErr)
	}

	val, err = store.Get(ctx, scope, "api_token")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("Get after delete = %q, want empty", val)
	}

	keys, err = store.List(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("List after delete = %v, want empty", keys)
	}
}

func TestGetNotFound(t *testing.T) {
	store, _, _ := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	val, err := store.Get(ctx, scope, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("Get nonexistent = %q, want empty", val)
	}
}

func TestSetOverwrite(t *testing.T) {
	store, _, _ := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	if err := store.Set(ctx, scope, "token", "v1", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, scope, "token", "v2", 0); err != nil {
		t.Fatal(err)
	}

	val, err := store.Get(ctx, scope, "token")
	if err != nil {
		t.Fatal(err)
	}
	if val != "v2" {
		t.Errorf("Get after overwrite = %q, want v2", val)
	}
}

func TestEncryptedAtRest(t *testing.T) {
	store, km, memStore := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	if err := store.Set(ctx, scope, "secret_key", "plaintext-secret", 0); err != nil {
		t.Fatal(err)
	}

	appName := secretAppName("test-agent")
	sessionID := deriveSessionID(km.IdentityKey(), appName, "alice", "secret_key")
	sess, err := memStore.Get(ctx, appName, "alice", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("session not found")
	}

	encB64, ok := sess.Metadata["secret_value"].(string)
	if !ok {
		t.Fatal("secret_value not found in metadata")
	}

	if encB64 == "plaintext-secret" {
		t.Error("secret_value is plaintext — encryption not working")
	}

	if _, err := base64.StdEncoding.DecodeString(encB64); err != nil {
		t.Errorf("secret_value is not valid base64: %v", err)
	}
}

func TestAgentIsolation(t *testing.T) {
	memStore := session.NewMemoryStore()
	ctx := context.Background()

	km1, err := identity.NewKeyManager(identity.Config{Mode: "manual", Key: "agent1-key-1234567890"}, "agent-1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	store1 := NewSessionStore(memStore, km1, nil)

	km2, err := identity.NewKeyManager(identity.Config{Mode: "manual", Key: "agent2-key-0987654321"}, "agent-2", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	store2 := NewSessionStore(memStore, km2, nil)

	scope1 := Scope{AgentName: "agent-1", AppName: "app", UserID: "alice"}
	scope2 := Scope{AgentName: "agent-2", AppName: "app", UserID: "alice"}

	if setErr := store1.Set(ctx, scope1, "token", "agent1-secret", 0); setErr != nil {
		t.Fatal(setErr)
	}

	_, err = store2.Get(ctx, scope1, "token")
	if err != nil {
		t.Fatal(err)
	}

	val, err := store2.Get(ctx, scope2, "token")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Error("agent-2 should not find agent-1's credential")
	}
}

func TestTTLExpiry(t *testing.T) {
	store, _, _ := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	if err := store.Set(ctx, scope, "expiring", "value", 1*time.Second); err != nil {
		t.Fatal(err)
	}

	val, err := store.Get(ctx, scope, "expiring")
	if err != nil {
		t.Fatal(err)
	}
	if val != "value" {
		t.Errorf("Get immediately = %q, want value", val)
	}

	time.Sleep(1100 * time.Millisecond)

	val, err = store.Get(ctx, scope, "expiring")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("Get after expiry = %q, want empty", val)
	}

	keys, err := store.List(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("List after expiry = %v, want empty", keys)
	}
}

func TestSoftDeleteAuditTrail(t *testing.T) {
	store, km, memStore := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	if err := store.Set(ctx, scope, "token", "secret", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, scope, "token"); err != nil {
		t.Fatal(err)
	}

	appName := secretAppName("test-agent")
	sessionID := deriveSessionID(km.IdentityKey(), appName, "alice", "token")
	sess, err := memStore.Get(ctx, appName, "alice", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("session should still exist (soft-delete)")
	}

	if _, ok := sess.Metadata["expired_at"]; !ok {
		t.Error("expected expired_at in metadata")
	}
	if reason, ok := sess.Metadata["expired_reason"].(string); !ok || reason != "credential_revoked" {
		t.Errorf("expired_reason = %q, want credential_revoked", reason)
	}
	if _, ok := sess.Metadata["secret_value"]; ok {
		t.Error("secret_value should be removed on soft-delete")
	}
}

func TestAppNamePrefix(t *testing.T) {
	if got := secretAppName("my-agent"); got != "secret_my-agent" {
		t.Errorf("secretAppName = %q, want secret_my-agent", got)
	}
}

func TestDeriveSessionIDDeterministic(t *testing.T) {
	id1 := deriveSessionID("key", "app", "user", "cred")
	id2 := deriveSessionID("key", "app", "user", "cred")
	if id1 != id2 {
		t.Error("session IDs should be deterministic")
	}

	if _, err := uuid.Parse(id1); err != nil {
		t.Errorf("session ID is not a valid UUID: %v", err)
	}

	id3 := deriveSessionID("key", "app", "user", "other")
	if id1 == id3 {
		t.Error("different keys should produce different session IDs")
	}
}

func TestMultipleCredentials(t *testing.T) {
	store, _, _ := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	creds := map[string]string{
		"github_token": "ghp_abc123",
		"aws_key":      "AKIA...",
		"slack_token":  "xoxb-...",
	}

	for k, v := range creds {
		if err := store.Set(ctx, scope, k, v, 0); err != nil {
			t.Fatalf("Set(%q): %v", k, err)
		}
	}

	keys, err := store.List(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Errorf("List returned %d keys, want 3", len(keys))
	}

	for k, v := range creds {
		got, err := store.Get(ctx, scope, k)
		if err != nil {
			t.Fatalf("Get(%q): %v", k, err)
		}
		if got != v {
			t.Errorf("Get(%q) = %q, want %q", k, got, v)
		}
	}
}

func TestDeleteNonexistent(t *testing.T) {
	store, _, _ := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	if err := store.Delete(ctx, scope, "nonexistent"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestGet_DecryptionFailure_ReturnsError(t *testing.T) {
	store, _, memStore := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	if err := store.Set(ctx, scope, "mykey", "sensitive-value", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	appName := secretAppName(scope.AgentName)
	sessionID := deriveSessionID(store.keyManager.IdentityKey(), appName, scope.UserID, "mykey")
	sess, err := memStore.Get(ctx, appName, scope.UserID, sessionID)
	if err != nil || sess == nil {
		t.Fatalf("fetch stored session: %v (sess=%v)", err, sess)
	}
	sess.Metadata["secret_value"] = base64.StdEncoding.EncodeToString([]byte("garbage-that-wont-decrypt"))
	if saveErr := memStore.Save(ctx, sess); saveErr != nil {
		t.Fatalf("save corrupted: %v", saveErr)
	}

	got, err := store.Get(ctx, scope, "mykey")
	if err == nil {
		t.Errorf("Get with corrupted ciphertext should error; got value=%q", got)
	}
}

func TestGet_MalformedMetadata_Rejected(t *testing.T) {
	store, _, memStore := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	if err := store.Set(ctx, scope, "mykey", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	appName := secretAppName(scope.AgentName)
	sessionID := deriveSessionID(store.keyManager.IdentityKey(), appName, scope.UserID, "mykey")
	sess, _ := memStore.Get(ctx, appName, scope.UserID, sessionID)
	delete(sess.Metadata, "secret_value")
	if err := memStore.Save(ctx, sess); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.Get(ctx, scope, "mykey")
	if err == nil && got != "" {
		t.Errorf("Get with missing secret_value should fail or return empty; got %q", got)
	}
}

func TestSet_ForceExpireAuditTrail(t *testing.T) {
	store, _, memStore := setupStore(t)
	ctx := context.Background()
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}

	if err := store.Set(ctx, scope, "mykey", "v1", 0); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := store.Set(ctx, scope, "mykey", "v2", 0); err != nil {
		t.Fatalf("second Set: %v", err)
	}

	appName := secretAppName(scope.AgentName)
	sessionID := deriveSessionID(store.keyManager.IdentityKey(), appName, scope.UserID, "mykey")
	sess, err := memStore.Get(ctx, appName, scope.UserID, sessionID)
	if err != nil || sess == nil {
		t.Fatalf("fetch stored session: %v", err)
	}

	if _, ok := sess.Metadata["replaced_at"]; !ok {
		t.Error("expected replaced_at on rotated credential")
	}
	if _, ok := sess.Metadata["prev_created_at"]; !ok {
		t.Error("expected prev_created_at on rotated credential")
	}
	if reason, _ := sess.Metadata["prev_expired_reason"].(string); reason != "force_expire" {
		t.Errorf("prev_expired_reason = %q, want force_expire", reason)
	}
}
