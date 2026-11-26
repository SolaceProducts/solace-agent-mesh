// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func (w *wakeLogText) warnCount(msg string) int {
	n := 0
	for _, line := range strings.Split(w.all(), "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, `msg="`+msg+`"`) {
			n++
		}
	}
	return n
}

func redirectTarget(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func redirecting(t *testing.T, location string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		http.Redirect(w, req, location, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestAWakeTokenCallThatIsRedirectedIsNotFollowedAndTheClientSecretAndHeadersAreNotReplayed(t *testing.T) {
	logs := captureAllLogs(t)
	elsewhere, replayed := redirectTarget(t)
	idp, tokenCalls := redirecting(t, elsewhere.URL+"/steal")
	runtimeSrv, wakeCalls := redirectTarget(t)
	r := newRig(t, map[string]any{"wake": map[string]any{
		"url": runtimeSrv.URL + "/wake", "token_url": idp.URL + "/token", "client_id": "app", "client_secret": wakeClientSecret,
		"headers": map[string]any{"X-Session": "session-value"},
	}}, nil)
	r.awaitWakeWatch()

	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return logs.warnCount("agent link wake token request failed") == 1 })
	r.submit("T2", "tok:alice:T2", nil)
	waitFor(t, func() bool { return logs.warnCount("agent link wake token request failed") == 2 })

	if n := replayed.Load(); n != 0 {
		t.Fatalf("the redirect target received %d requests: the client secret was sent where the token endpoint pointed", n)
	}
	if n := tokenCalls.Load(); n != 2 {
		t.Fatalf("the token endpoint was called %d times, want one per wake attempt", n)
	}
	if n := wakeCalls.Load(); n != 0 {
		t.Fatalf("the runtime was called %d times without a token", n)
	}
	requireNoSecretsLogged(t, logs.all())
}

func TestAWakeCallThatIsRedirectedIsNotFollowedAndItsHeadersAreNotReplayed(t *testing.T) {
	logs := captureAllLogs(t)
	elsewhere, replayed := redirectTarget(t)
	runtimeSrv, wakeCalls := redirecting(t, elsewhere.URL+"/steal")
	r := newRig(t, map[string]any{"wake": map[string]any{
		"url": runtimeSrv.URL + "/wake", "headers": map[string]any{"X-Session": "session-value"},
	}}, nil)
	r.awaitWakeWatch()

	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return logs.warnCount("agent link wake call refused") == 1 || replayed.Load() > 0 })
	r.submit("T2", "tok:alice:T2", nil)
	waitFor(t, func() bool { return logs.warnCount("agent link wake call refused") == 2 || replayed.Load() > 0 })

	if n := replayed.Load(); n != 0 {
		t.Fatalf("the redirect target received %d requests: the wake headers were sent where the runtime pointed", n)
	}
	if n := wakeCalls.Load(); n != 2 {
		t.Fatalf("the runtime was called %d times, want one per wake attempt", n)
	}
}
