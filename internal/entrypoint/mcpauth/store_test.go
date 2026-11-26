// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/atrest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func newDBStore(t *testing.T, db *store.DB, gatewayID string) *dbStore {
	t.Helper()
	box, err := atrest.New("test-secret", "mcp-oauth")
	if err != nil {
		t.Fatal(err)
	}
	return &dbStore{repo: store.NewMCPOAuthRepo(db), box: box, gatewayID: gatewayID, logger: slog.New(slog.DiscardHandler)}
}

func eachStore(t *testing.T, fn func(t *testing.T, s recordStore)) {
	t.Run("memory", func(t *testing.T) { fn(t, newMemoryStore()) })
	t.Run("database", func(t *testing.T) { fn(t, newDBStore(t, store.TestDB(t), "gw")) })
}

func liveState(verifier string) *oauthState {
	return &oauthState{CodeVerifier: verifier, ClientRedirectURI: "http://localhost/cb", CreatedAt: time.Now(), TTL: time.Minute}
}

func TestStore_StateTakenOnce(t *testing.T) {
	eachStore(t, func(t *testing.T, s recordStore) {
		ctx := t.Context()
		if err := s.putState(ctx, "k1", liveState("v1")); err != nil {
			t.Fatal(err)
		}
		if n, err := s.countLiveStates(ctx); err != nil || n != 1 {
			t.Fatalf("CountLiveStates = %d, %v", n, err)
		}
		got, err := s.takeState(ctx, "k1")
		if err != nil || got == nil || got.CodeVerifier != "v1" || got.ClientRedirectURI != "http://localhost/cb" {
			t.Fatalf("TakeState = %+v, %v", got, err)
		}
		if got, _ := s.takeState(ctx, "k1"); got != nil {
			t.Error("second TakeState found the state")
		}
	})
}

func TestStore_StateTakenByVerifierHash(t *testing.T) {
	eachStore(t, func(t *testing.T, s recordStore) {
		ctx := t.Context()
		_ = s.putState(ctx, "k1", liveState("v1"))
		if got, _ := s.takeStateByVerifierHash(ctx, "deadbeef"); got != nil {
			t.Error("unknown hash found a state")
		}
		got, err := s.takeStateByVerifierHash(ctx, verifierHash("v1"))
		if err != nil || got == nil || got.CodeVerifier != "v1" {
			t.Fatalf("TakeStateByVerifierHash = %+v, %v", got, err)
		}
		if got, _ := s.takeState(ctx, "k1"); got != nil {
			t.Error("state still there after take by verifier")
		}
	})
}

func TestStore_ExpiredRecordsMiss(t *testing.T) {
	eachStore(t, func(t *testing.T, s recordStore) {
		ctx := t.Context()
		old := time.Now().Add(-time.Hour)
		_ = s.putState(ctx, "k1", &oauthState{CodeVerifier: "v1", CreatedAt: old, TTL: time.Minute})
		_ = s.putCode(ctx, "c1", &oauthCode{CreatedAt: old, TTL: time.Minute})
		_ = s.putRefresh(ctx, "r1", &mcpRefreshTokenEntry{IDPRefreshToken: "idp", CreatedAt: old, TTL: time.Minute})
		if n, _ := s.countLiveStates(ctx); n != 0 {
			t.Errorf("CountLiveStates = %d, want 0", n)
		}
		if got, _ := s.takeStateByVerifierHash(ctx, verifierHash("v1")); got != nil {
			t.Error("expired state taken by verifier")
		}
		if got, _ := s.takeCode(ctx, "c1"); got != nil {
			t.Error("expired code taken")
		}
		if got, _ := s.getRefresh(ctx, "r1"); got != nil {
			t.Error("expired refresh found")
		}
		if ok, _ := s.rotateRefresh(ctx, "r1", "r2", &mcpRefreshTokenEntry{CreatedAt: time.Now(), TTL: time.Hour}); ok {
			t.Error("expired refresh rotated")
		}
	})
}

func TestStore_CodeTakenOnce(t *testing.T) {
	eachStore(t, func(t *testing.T, s recordStore) {
		ctx := t.Context()
		_ = s.putCode(ctx, "c1", &oauthCode{AccessToken: "sam", IDPRefreshToken: "idp", RedirectURI: "u", CreatedAt: time.Now(), TTL: time.Minute})
		got, err := s.takeCode(ctx, "c1")
		if err != nil || got == nil || got.AccessToken != "sam" || got.IDPRefreshToken != "idp" {
			t.Fatalf("TakeCode = %+v, %v", got, err)
		}
		if got, _ := s.takeCode(ctx, "c1"); got != nil {
			t.Error("second TakeCode found the code")
		}
	})
}

func TestStore_RefreshRotation(t *testing.T) {
	eachStore(t, func(t *testing.T, s recordStore) {
		ctx := t.Context()
		_ = s.putRefresh(ctx, "r1", &mcpRefreshTokenEntry{IDPRefreshToken: "idp1", CreatedAt: time.Now(), TTL: time.Hour})
		got, err := s.getRefresh(ctx, "r1")
		if err != nil || got == nil || got.IDPRefreshToken != "idp1" {
			t.Fatalf("GetRefresh = %+v, %v", got, err)
		}
		ok, err := s.rotateRefresh(ctx, "r1", "r2", &mcpRefreshTokenEntry{IDPRefreshToken: "idp2", CreatedAt: time.Now(), TTL: time.Hour})
		if err != nil || !ok {
			t.Fatalf("RotateRefresh = %v, %v", ok, err)
		}
		if got, _ := s.getRefresh(ctx, "r1"); got != nil {
			t.Error("old refresh token still valid")
		}
		if got, _ := s.getRefresh(ctx, "r2"); got == nil || got.IDPRefreshToken != "idp2" {
			t.Errorf("new refresh = %+v", got)
		}
		if ok, _ := s.rotateRefresh(ctx, "r1", "r3", &mcpRefreshTokenEntry{CreatedAt: time.Now(), TTL: time.Hour}); ok {
			t.Error("rotated an already rotated token")
		}
	})
}

func TestStore_ConcurrentRotationWinsOnce(t *testing.T) {
	eachStore(t, func(t *testing.T, s recordStore) {
		ctx := t.Context()
		_ = s.putRefresh(ctx, "r1", &mcpRefreshTokenEntry{IDPRefreshToken: "idp", CreatedAt: time.Now(), TTL: time.Hour})
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for _, next := range []string{"a", "b", "c", "d"} {
			wg.Go(func() {
				ok, err := s.rotateRefresh(ctx, "r1", next, &mcpRefreshTokenEntry{IDPRefreshToken: next, CreatedAt: time.Now(), TTL: time.Hour})
				if err != nil {
					t.Errorf("RotateRefresh: %v", err)
				}
				if ok {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		if wins != 1 {
			t.Errorf("wins = %d, want 1", wins)
		}
	})
}

func TestDBStore_GatewaysAreIsolated(t *testing.T) {
	db := store.TestDB(t)
	a, b := newDBStore(t, db, "gw-a"), newDBStore(t, db, "gw-b")
	ctx := t.Context()
	_ = a.putState(ctx, "k1", liveState("v1"))
	_ = a.putCode(ctx, "c1", &oauthCode{CreatedAt: time.Now(), TTL: time.Minute})
	_ = a.putRefresh(ctx, "r1", &mcpRefreshTokenEntry{CreatedAt: time.Now(), TTL: time.Hour})
	if n, _ := b.countLiveStates(ctx); n != 0 {
		t.Errorf("gw-b counts gw-a's states: %d", n)
	}
	if got, _ := b.takeState(ctx, "k1"); got != nil {
		t.Error("gw-b took gw-a's state")
	}
	if got, _ := b.takeStateByVerifierHash(ctx, verifierHash("v1")); got != nil {
		t.Error("gw-b took gw-a's state by verifier")
	}
	if got, _ := b.takeCode(ctx, "c1"); got != nil {
		t.Error("gw-b took gw-a's code")
	}
	if got, _ := b.getRefresh(ctx, "r1"); got != nil {
		t.Error("gw-b read gw-a's refresh token")
	}
	if got, _ := a.takeState(ctx, "k1"); got == nil {
		t.Error("gw-a lost its own state")
	}
}

func TestDBStore_NoSecretOrClientValueInTables(t *testing.T) {
	db := store.TestDB(t)
	s := newDBStore(t, db, "gw")
	ctx := t.Context()
	_ = s.putState(ctx, "state-value", liveState("verifier-secret"))
	_ = s.putCode(ctx, "code-value", &oauthCode{AccessToken: "sam-access-secret", IDPRefreshToken: "idp-rt-secret", CreatedAt: time.Now(), TTL: time.Minute})
	_ = s.putRefresh(ctx, "client-rt-value", &mcpRefreshTokenEntry{IDPRefreshToken: "idp-rt-secret", CreatedAt: time.Now(), TTL: time.Hour})
	for _, q := range []string{
		`SELECT state_hash || verifier_hash || data FROM mcp_oauth_state`,
		`SELECT code_hash || data FROM mcp_oauth_code`,
		`SELECT token_hash || data FROM mcp_oauth_refresh`,
	} {
		var row string
		if err := db.QueryRowContext(ctx, q).Scan(&row); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		for _, secret := range []string{"state-value", "verifier-secret", "code-value", "sam-access-secret", "idp-rt-secret", "client-rt-value"} {
			if strings.Contains(row, secret) {
				t.Errorf("%s holds %q in the clear", q, secret)
			}
		}
	}
}

func TestDBStore_UnreadableRecordReadsAsAbsent(t *testing.T) {
	db := store.TestDB(t)
	s := newDBStore(t, db, "gw")
	ctx := t.Context()
	now := time.Now()
	_ = s.putState(ctx, "k1", liveState("v1"))
	_ = s.putCode(ctx, "c1", &oauthCode{CreatedAt: now, TTL: time.Minute})
	_ = s.putRefresh(ctx, "r1", &mcpRefreshTokenEntry{IDPRefreshToken: "idp", CreatedAt: now, TTL: time.Hour})
	other, _ := atrest.New("other-secret", "mcp-oauth")
	s.box = other
	if got, err := s.takeState(ctx, "k1"); err != nil || got != nil {
		t.Errorf("takeState with the wrong key = %+v, %v; want nil, nil", got, err)
	}
	if got, err := s.takeCode(ctx, "c1"); err != nil || got != nil {
		t.Errorf("takeCode with the wrong key = %+v, %v; want nil, nil", got, err)
	}
	if got, err := s.getRefresh(ctx, "r1"); err != nil || got != nil {
		t.Errorf("getRefresh with the wrong key = %+v, %v; want nil, nil", got, err)
	}
}
