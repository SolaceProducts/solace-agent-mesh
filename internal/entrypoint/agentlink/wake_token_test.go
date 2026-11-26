// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestAWakeRefusedWithUnauthorizedFetchesANewTokenForTheNextWake(t *testing.T) {
	for _, refusal := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(refusal), func(t *testing.T) {
			var fetched, wakes atomic.Int32
			idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fetched.Add(1)
				_, _ = w.Write([]byte(`{"access_token":"wake-token","expires_in":3600}`))
			}))
			t.Cleanup(idp.Close)
			runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if wakes.Add(1) == 1 {
					w.WriteHeader(refusal)
				}
			}))
			t.Cleanup(runtimeSrv.Close)
			r := newRig(t, map[string]any{"wake": map[string]any{"url": runtimeSrv.URL, "token_url": idp.URL,
				"client_id": "app", "client_secret": "wake-secret", "on_start": false}}, nil)
			r.awaitWakeWatch()

			r.submit("T1", "tok:alice:T1", nil)
			waitFor(t, func() bool { return wakes.Load() == 1 })
			waitFor(t, func() bool {
				done := false
				r.locked(func(s *agentState) { done = !s.awaitingConnect })
				return done
			})
			r.submit("T2", "tok:alice:T2", nil)
			waitFor(t, func() bool { return wakes.Load() == 2 })
			if got := fetched.Load(); got != 2 {
				t.Fatalf("the token was fetched %d times, want 2: a refused token was used again", got)
			}
		})
	}
}
