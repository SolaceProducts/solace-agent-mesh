// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestKeyManagerWithSeedOrPersistence_NoSeedNoPersistDir_Ephemeral(t *testing.T) {
	t.Parallel()

	a, err := NewKeyManagerWithSeedOrPersistence("agent-1", SigningKeyConfig{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKeyManagerWithSeedOrPersistence("agent-1", SigningKeyConfig{})
	if err != nil {
		t.Fatal(err)
	}

	if a.Origin() != KeyOriginEphemeral {
		t.Errorf("Origin() = %v, want KeyOriginEphemeral", a.Origin())
	}
	if a.CurrentKeyPair().PublicKey.Equal(b.CurrentKeyPair().PublicKey) {
		t.Error("with neither seed nor persistDir, keys should differ across constructions")
	}
}

func TestKeyManagerWithSeedOrPersistence_FirstBoot_GeneratesAndPersists(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "keys")

	km, err := NewKeyManagerWithSeedOrPersistence("gw-webui", SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if km.Origin() != KeyOriginPersistedGenerated {
		t.Errorf("Origin() = %v, want KeyOriginPersistedGenerated", km.Origin())
	}

	wantPath := filepath.Join(dir, "trust_signing_gw-webui.pem")
	if km.PersistedPath() != wantPath {
		t.Errorf("PersistedPath() = %q, want %q", km.PersistedPath(), wantPath)
	}

	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("expected key file at %s: %v", wantPath, err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("key file mode = %v, want 0600", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0077 != 0 {
		t.Errorf("key dir mode = %v, want no group/world access", perm)
	}
}

func TestKeyManagerWithSeedOrPersistence_SecondConstruction_LoadsSameKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := NewKeyManagerWithSeedOrPersistence("gw-webui", SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewKeyManagerWithSeedOrPersistence("gw-webui", SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	if second.Origin() != KeyOriginPersistedLoaded {
		t.Errorf("Origin() = %v, want KeyOriginPersistedLoaded", second.Origin())
	}
	kp1, kp2 := first.CurrentKeyPair(), second.CurrentKeyPair()
	if !kp1.PrivateKey.Equal(kp2.PrivateKey) {
		t.Error("second construction must load the same private key persisted by the first")
	}
	if kp1.KeyID != kp2.KeyID {
		t.Errorf("kid must be stable across restarts: %q vs %q", kp1.KeyID, kp2.KeyID)
	}
}

func TestKeyManagerWithSeedOrPersistence_SeedTakesPriorityOverPersistDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	seed := []byte("a-stable-session-secret-key-value")

	km, err := NewKeyManagerWithSeedOrPersistence("gw-webui", SigningKeyConfig{Seed: seed, PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if km.Origin() != KeyOriginSeedDerived {
		t.Errorf("Origin() = %v, want KeyOriginSeedDerived", km.Origin())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("persistDir should stay untouched when seed is set, found %d entries", len(entries))
	}
}

func TestKeyManagerWithSeedOrPersistence_CorruptFile_DegradesWithoutOverwriting(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	wrongCurveKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongCurveDER, err := x509.MarshalECPrivateKey(wrongCurveKey)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		content []byte
	}{
		{"garbage bytes", []byte("this is not a PEM file at all")},
		{"empty file", []byte{}},
		{"wrong curve (P-384)", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: wrongCurveDER})},
		{"wrong key type (RSA)", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "trust_signing_gw-corrupt.pem")
			if err := os.WriteFile(path, tt.content, 0600); err != nil {
				t.Fatal(err)
			}

			km, err := NewKeyManagerWithSeedOrPersistence("gw-corrupt", SigningKeyConfig{PersistenceDir: dir})
			if err != nil {
				t.Fatalf("construction should degrade, not fail: %v", err)
			}
			if km.Origin() != KeyOriginEphemeralDegraded {
				t.Errorf("Origin() = %v, want KeyOriginEphemeralDegraded", km.Origin())
			}
			if km.DegradedError() == nil {
				t.Error("DegradedError() should be non-nil after a corrupt persisted file")
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(tt.content) {
				t.Error("corrupt file must be left untouched, not silently overwritten")
			}
		})
	}
}

func TestKeyManagerWithSeedOrPersistence_WriteFailure_Degrades(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root bypasses directory permission checks")
	}

	parent := t.TempDir()
	readOnlyDir := filepath.Join(parent, "readonly")
	if err := os.Mkdir(readOnlyDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0700) })

	unwritable := filepath.Join(readOnlyDir, "keys")

	km, err := NewKeyManagerWithSeedOrPersistence("gw-webui", SigningKeyConfig{PersistenceDir: unwritable})
	if err != nil {
		t.Fatalf("construction should degrade, not fail: %v", err)
	}
	if km.Origin() != KeyOriginEphemeralDegraded {
		t.Errorf("Origin() = %v, want KeyOriginEphemeralDegraded", km.Origin())
	}
	if km.DegradedError() == nil {
		t.Error("DegradedError() should be non-nil after a write failure")
	}
}

func TestKeyManagerWithSeedOrPersistence_PathTraversalComponentID_Degrades(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	dir := filepath.Join(parent, "keys")

	km, err := NewKeyManagerWithSeedOrPersistence("../escape", SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatalf("construction should degrade, not fail: %v", err)
	}
	if km.Origin() != KeyOriginEphemeralDegraded {
		t.Errorf("Origin() = %v, want KeyOriginEphemeralDegraded", km.Origin())
	}
	if km.DegradedError() == nil {
		t.Error("DegradedError() should be non-nil for a path-escaping componentID")
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "keys" {
			t.Errorf("no file should be written outside persistDir, found %q under %s", e.Name(), parent)
		}
	}
}

func TestKeyManagerWithSeedOrPersistence_DistinctComponentIDs_DistinctFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	a, err := NewKeyManagerWithSeedOrPersistence("gw-webui", SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKeyManagerWithSeedOrPersistence("gw-mcp", SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	if a.PersistedPath() == b.PersistedPath() {
		t.Errorf("distinct componentIDs must persist to distinct files, both got %q", a.PersistedPath())
	}
	if a.CurrentKeyPair().PublicKey.Equal(b.CurrentKeyPair().PublicKey) {
		t.Error("distinct componentIDs must get distinct keys")
	}
}

func TestKeyManagerWithSeedOrPersistence_SurvivesRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const id = "gw-webui"

	kmBefore, err := NewKeyManagerWithSeedOrPersistence(id, SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewSigner(kmBefore, id, time.Hour, time.Hour).Sign(SignClaims{Subject: "user@example.com"})
	if err != nil {
		t.Fatal(err)
	}

	kmAfter, err := NewKeyManagerWithSeedOrPersistence(id, SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	pub := NewSigner(kmAfter, id, time.Hour, time.Hour).PublicKey()

	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return pub, nil })
	if err != nil {
		t.Fatalf("token signed before restart must verify after restart: %v", err)
	}
	if !parsed.Valid {
		t.Error("token should be valid after restart")
	}
}

func TestKeyManagerWithSeedOrPersistence_RotateKey_NotRePersisted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const id = "gw-webui"

	km, err := NewKeyManagerWithSeedOrPersistence(id, SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	baseKID := km.CurrentKeyPair().KeyID

	if _, rotateErr := km.RotateKey(); rotateErr != nil {
		t.Fatal(rotateErr)
	}
	rotatedKID := km.CurrentKeyPair().KeyID
	if rotatedKID == baseKID {
		t.Fatal("RotateKey should install a new current key (test setup problem)")
	}

	restarted, err := NewKeyManagerWithSeedOrPersistence(id, SigningKeyConfig{PersistenceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.CurrentKeyPair().KeyID; got != baseKID {
		t.Errorf("restart should reload the persisted BASE key %q, got %q (rotated kid was %q)", baseKID, got, rotatedKID)
	}
	if restarted.Origin() != KeyOriginPersistedLoaded {
		t.Errorf("Origin() = %v, want KeyOriginPersistedLoaded", restarted.Origin())
	}
}

func TestKeyManagerWithSeedOrPersistence_ConcurrentFirstBoot_AllReplicasAgreeWithDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const id = "gw-webui"
	const n = 8

	results := make([]*KeyManager, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			km, err := NewKeyManagerWithSeedOrPersistence(id, SigningKeyConfig{PersistenceDir: dir})
			if err != nil {
				t.Errorf("goroutine construction failed: %v", err)
				return
			}
			results[i] = km
		})
	}
	wg.Wait()

	onDisk, err := loadPersistedKey(filepath.Join(dir, "trust_signing_gw-webui.pem"))
	if err != nil {
		t.Fatal(err)
	}

	for i, km := range results {
		if km == nil {
			continue
		}
		if km.Origin() != KeyOriginPersistedGenerated && km.Origin() != KeyOriginPersistedLoaded {
			t.Errorf("goroutine %d: Origin() = %v, want a persisted-* value", i, km.Origin())
		}
		if !km.CurrentKeyPair().PrivateKey.Equal(onDisk) {
			t.Errorf("goroutine %d: in-memory key does not match what's on disk — a losing writer kept its own key instead of reloading", i)
		}
	}
}

func TestReloadRaceWinnerKey_MissingFile_ReturnsImmediately(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "trust_signing_never-claimed.pem")

	start := time.Now()
	_, err := reloadRaceWinnerKey(path)
	elapsed := time.Since(start)

	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("took %v, want an immediate return on ErrNotExist, not the full retry budget", elapsed)
	}
}

func TestReloadRaceWinnerKey_PermanentlyCorruptFile_ExhaustsRetries(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "trust_signing_abandoned.pem")
	if err := os.WriteFile(path, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}

	_, err := reloadRaceWinnerKey(path)
	if err == nil {
		t.Fatal("expected an error for a file that never becomes valid")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Error("err should be the parse failure, not ErrNotExist — the file exists")
	}
}
