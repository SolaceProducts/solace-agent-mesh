// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

func TestAutoModeFileGenerate(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "test-agent.key")

	cfg := Config{Mode: "auto", FilePath: keyPath}
	km, err := NewKeyManager(cfg, "test-agent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(km.IdentityKey()) != 64 {
		t.Errorf("expected 64-char hex key, got %d chars", len(km.IdentityKey()))
	}
	if len(km.EncryptionKey()) != 32 {
		t.Errorf("expected 32-byte encryption key, got %d bytes", len(km.EncryptionKey()))
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("key file not created: %v", err)
	}
	if got := trimNewline(string(data)); got != km.IdentityKey() {
		t.Errorf("file content mismatch: got %q, want %q", got, km.IdentityKey())
	}
}

func TestAutoModeFileReload(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "test-agent.key")

	cfg := Config{Mode: "auto", FilePath: keyPath}

	km1, err := NewKeyManager(cfg, "test-agent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	km2, err := NewKeyManager(cfg, "test-agent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if km1.IdentityKey() != km2.IdentityKey() {
		t.Error("key should be the same after reload")
	}
}

func TestAutoModeFilePermissions(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "test-agent.key")

	cfg := Config{Mode: "auto", FilePath: keyPath}
	_, err := NewKeyManager(cfg, "test-agent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

func TestAutoModeDBPersist(t *testing.T) {
	store := session.NewMemoryStore()

	cfg := Config{Mode: "auto"}
	km1, err := NewKeyManager(cfg, "test-agent", store, nil)
	if err != nil {
		t.Fatal(err)
	}

	km2, err := NewKeyManager(cfg, "test-agent", store, nil)
	if err != nil {
		t.Fatal(err)
	}

	if km1.IdentityKey() != km2.IdentityKey() {
		t.Error("key should be the same after DB reload")
	}
}

func TestManualModeValid(t *testing.T) {
	cfg := Config{Mode: "manual", Key: "abcdef1234567890"}
	km, err := NewKeyManager(cfg, "test-agent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if km.IdentityKey() != "abcdef1234567890" {
		t.Errorf("identity key = %q, want %q", km.IdentityKey(), "abcdef1234567890")
	}
}

func TestManualModeLong(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cfg := Config{Mode: "manual", Key: key}
	km, err := NewKeyManager(cfg, "test-agent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if km.IdentityKey() != key {
		t.Error("identity key mismatch")
	}
}

func TestManualModeTooShort(t *testing.T) {
	cfg := Config{Mode: "manual", Key: "short"}
	_, err := NewKeyManager(cfg, "test-agent", nil, nil)
	if err == nil {
		t.Error("expected error for key shorter than 16 chars")
	}
}

func TestManualModeMinLength(t *testing.T) {
	cfg := Config{Mode: "manual", Key: "1234567890123456"}
	_, err := NewKeyManager(cfg, "test-agent", nil, nil)
	if err != nil {
		t.Fatalf("16-char key should be accepted: %v", err)
	}
}

func TestTwoAgentsDifferentKeys(t *testing.T) {
	store := session.NewMemoryStore()
	cfg := Config{Mode: "auto"}

	km1, err := NewKeyManager(cfg, "agent-1", store, nil)
	if err != nil {
		t.Fatal(err)
	}

	km2, err := NewKeyManager(cfg, "agent-2", store, nil)
	if err != nil {
		t.Fatal(err)
	}

	if km1.IdentityKey() == km2.IdentityKey() {
		t.Error("two agents should have different identity keys")
	}
}

func TestEncryptionKeyDeterministic(t *testing.T) {
	cfg := Config{Mode: "manual", Key: "deterministic-test-key-1234"}

	km1, err := NewKeyManager(cfg, "agent-a", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	km2, err := NewKeyManager(cfg, "agent-b", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if string(km1.EncryptionKey()) != string(km2.EncryptionKey()) {
		t.Error("same identity key should produce same encryption key")
	}

	expected := sha256.Sum256([]byte("deterministic-test-key-1234"))
	if string(km1.EncryptionKey()) != string(expected[:]) {
		t.Error("encryption key should be SHA256 of identity key")
	}
}

func TestInvalidMode(t *testing.T) {
	cfg := Config{Mode: "invalid"}
	_, err := NewKeyManager(cfg, "test", nil, nil)
	if err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestAgentName(t *testing.T) {
	cfg := Config{Mode: "manual", Key: "1234567890123456"}
	km, err := NewKeyManager(cfg, "my-agent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if km.AgentName() != "my-agent" {
		t.Errorf("AgentName() = %q, want %q", km.AgentName(), "my-agent")
	}
}

func trimNewline(s string) string {
	return s[:len(s)-1]
}
