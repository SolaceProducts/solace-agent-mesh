// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type refreshProvider struct {
	refreshes    atomic.Int64
	firstArrived chan struct{}
	release      chan struct{}
}

func newRefreshProvider(t *testing.T, firstStatus int, firstBody string) (*refreshProvider, string) {
	t.Helper()
	p := &refreshProvider{firstArrived: make(chan struct{}), release: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if p.refreshes.Add(1) > 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		close(p.firstArrived)
		<-p.release
		w.WriteHeader(firstStatus)
		_, _ = w.Write([]byte(firstBody))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	})
	return p, srv.URL
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(twoWritersWait):
		t.Fatalf("%s did not happen within %v", what, twoWritersWait)
	}
}

type plainRows struct {
	mu   sync.Mutex
	rows map[string]string
}

type plainStore struct {
	rows   *plainRows
	onRead func()
}

func newPlainStores() (first, second *plainStore) {
	rows := &plainRows{rows: map[string]string{}}
	return &plainStore{rows: rows}, &plainStore{rows: rows}
}

func plainKey(agentName, appName, userID, key string) string {
	return strings.Join([]string{agentName, appName, userID, key}, "|")
}

func (s *plainStore) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	s.rows.mu.Lock()
	value := s.rows.rows[plainKey(agentName, appName, userID, key)]
	s.rows.mu.Unlock()
	if s.onRead != nil {
		s.onRead()
	}
	return value, nil
}

func (s *plainStore) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	s.rows.rows[plainKey(agentName, appName, userID, key)] = value
	return nil
}

func (s *plainStore) Delete(_ context.Context, agentName, appName, userID, key string) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	delete(s.rows.rows, plainKey(agentName, appName, userID, key))
	return nil
}

func (s *plainStore) List(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}

type readSignalStore struct {
	*interceptedStore
	onRead func()
}

func (s *readSignalStore) GetVersioned(ctx context.Context, ref auth.CredentialRef) (auth.VersionedCredential, error) {
	read, err := s.interceptedStore.GetVersioned(ctx, ref)
	if s.onRead != nil {
		s.onRead()
	}
	return read, err
}

func signalFirstRead(done chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

func readSignalledStores(t *testing.T, versioned bool, secondRead chan struct{}) (first, second auth.CredentialService) {
	t.Helper()
	if !versioned {
		plainFirst, plainSecond := newPlainStores()
		plainSecond.onRead = signalFirstRead(secondRead)
		return plainFirst, plainSecond
	}
	shared, other := sharedCredentialStores(t)
	return shared, &readSignalStore{interceptedStore: interceptStore(t, other), onRead: signalFirstRead(secondRead)}
}

func storedOAuthToken(t *testing.T, svc auth.CredentialService) OAuthToken {
	t.Helper()
	raw, err := svc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "test_key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	var stored OAuthToken
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			t.Fatalf("stored token %q does not decode: %v", raw, err)
		}
	}
	return stored
}

func TestRefreshOfOneExpiredTokenReachesTheProviderOnce(t *testing.T) {
	tests := []struct {
		name          string
		oneProcess    bool
		versionedRows bool
	}{
		{name: "calls in one process share one refresh when the store has no versions", oneProcess: true},
		{name: "a process that finds another process's lease waits and uses the token it stored", versionedRows: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, url := newRefreshProvider(t, http.StatusOK, freshAccessResponse)
			secondRead := make(chan struct{})
			firstSvc, secondSvc := readSignalledStores(t, tt.versionedRows, secondRead)
			setStoredToken(t, firstSvc, expiredToken("old-access"))
			first, second := staticClientReplica(url), staticClientReplica(url)
			if tt.oneProcess {
				shared := new(singleflight.Group)
				first.refreshFlights, second.refreshFlights = shared, shared
			} else {
				inOwnProcess(first)
				inOwnProcess(second)
			}
			ctx := t.Context()

			results := [2]chan tokenResult{make(chan tokenResult, 1), make(chan tokenResult, 1)}
			start := func(i int, h *AuthHandler, svc auth.CredentialService) {
				go func() {
					tok, err := h.GetOAuthToken(ctx, replicaToolCtx(svc))
					results[i] <- tokenResult{token: tok, err: err}
				}()
			}
			start(0, first, firstSvc)
			waitClosed(t, provider.firstArrived, "the first refresh reaching the provider")
			start(1, second, secondSvc)
			waitClosed(t, secondRead, "the second caller reading the expired token")
			close(provider.release)

			for i := range 2 {
				got := waitToken(t, results[i])
				if got.err != nil || got.token.AccessToken != "fresh-access" {
					t.Errorf("caller %d GetOAuthToken() = %+v, want access token %q", i, got, "fresh-access")
				}
			}
			if n := provider.refreshes.Load(); n != 1 {
				t.Errorf("provider received %d refreshes, want 1", n)
			}
			if stored := storedOAuthToken(t, firstSvc); stored.AccessToken != "fresh-access" || stored.RefreshStartedAt != 0 {
				t.Errorf("stored token = %+v, want access token %q and no lease", stored, "fresh-access")
			}
		})
	}
}

func TestRefreshLeaseLeftBehind(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		leaseAge   time.Duration
		wantAccess string
		wantErr    bool
	}{
		{
			name:       "a lease older than its lifetime is taken over",
			status:     http.StatusOK,
			body:       freshAccessResponse,
			leaseAge:   refreshLeaseTTL + time.Second,
			wantAccess: "fresh-access",
		},
		{
			name:       "a holder whose refresh fails for a moment removes its lease",
			status:     http.StatusServiceUnavailable,
			body:       `{"error":"temporarily_unavailable"}`,
			wantAccess: "old-access",
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, url := newRefreshProvider(t, tt.status, tt.body)
			close(provider.release)
			svc, _ := sharedCredentialStores(t)
			left := expiredToken("old-access")
			if tt.leaseAge > 0 {
				left.RefreshStartedAt = time.Now().Add(-tt.leaseAge).UnixMilli()
			}
			setStoredToken(t, svc, left)
			ctx, cancel := context.WithTimeout(t.Context(), twoWritersWait)
			defer cancel()

			got, err := inOwnProcess(staticClientReplica(url)).GetOAuthToken(ctx, replicaToolCtx(svc))

			if (err != nil) != tt.wantErr || (err == nil && got.AccessToken != tt.wantAccess) {
				t.Errorf("GetOAuthToken() = (%+v, %v), want access token %q, error %v", got, err, tt.wantAccess, tt.wantErr)
			}
			if stored := storedOAuthToken(t, svc); stored.AccessToken != tt.wantAccess || stored.RefreshStartedAt != 0 {
				t.Errorf("stored token = %+v, want access token %q and no lease", stored, tt.wantAccess)
			}
		})
	}
}

func TestTwoProcessesReadTheSameVersionAndRaceForTheLease(t *testing.T) {
	provider, url := newRefreshProvider(t, http.StatusOK, freshAccessResponse)
	shared, other := sharedCredentialStores(t)
	firstSvc := interceptStore(t, shared)
	setStoredToken(t, firstSvc, expiredToken("old-access"))
	first, second := inOwnProcess(staticClientReplica(url)), inOwnProcess(staticClientReplica(url))
	ctx := t.Context()

	results := [2]chan tokenResult{make(chan tokenResult, 1), make(chan tokenResult, 1)}
	firstSvc.beforeSetAt = func(context.Context, auth.CredentialRef) {
		go func() {
			tok, err := second.GetOAuthToken(ctx, replicaToolCtx(other))
			results[1] <- tokenResult{token: tok, err: err}
		}()
		waitClosed(t, provider.firstArrived, "the second process's refresh reaching the provider")
	}
	go func() {
		tok, err := first.GetOAuthToken(ctx, replicaToolCtx(firstSvc))
		results[0] <- tokenResult{token: tok, err: err}
	}()
	waitClosed(t, provider.firstArrived, "the second process's refresh reaching the provider")
	close(provider.release)

	for i := range 2 {
		got := waitToken(t, results[i])
		if got.err != nil || got.token.AccessToken != "fresh-access" {
			t.Errorf("caller %d GetOAuthToken() = %+v, want access token %q", i, got, "fresh-access")
		}
	}
	if n := provider.refreshes.Load(); n != 1 {
		t.Errorf("provider received %d refreshes, want 1", n)
	}
	if stored := storedOAuthToken(t, firstSvc); stored.AccessToken != "fresh-access" || stored.RefreshStartedAt != 0 {
		t.Errorf("stored token = %+v, want access token %q and no lease", stored, "fresh-access")
	}
}

func TestRefreshedTokenWhoseFirstWriteFailsIsStored(t *testing.T) {
	provider, url := newRefreshProvider(t, http.StatusOK, freshAccessResponse)
	close(provider.release)
	shared, _ := sharedCredentialStores(t)
	svc := interceptStore(t, shared)
	setStoredToken(t, svc, expiredToken("old-access"))
	failed := false
	svc.failSetAt = func(value string) error {
		if !failed && strings.Contains(value, "fresh-access") {
			failed = true
			return errors.New("connection reset by peer")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), twoWritersWait)
	defer cancel()

	got, err := inOwnProcess(staticClientReplica(url)).GetOAuthToken(ctx, replicaToolCtx(svc))

	if err != nil || got.AccessToken != "fresh-access" {
		t.Fatalf("GetOAuthToken() = (%+v, %v), want access token %q", got, err, "fresh-access")
	}
	if !failed {
		t.Fatal("the write of the refreshed token was never failed")
	}
	if stored := storedOAuthToken(t, svc); stored.AccessToken != "fresh-access" || stored.RefreshStartedAt != 0 {
		t.Errorf("stored token = %+v, want access token %q and no lease", stored, "fresh-access")
	}
}
