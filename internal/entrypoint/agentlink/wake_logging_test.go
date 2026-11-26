// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	wakeQuery          = "?api-version=v1&code=shh"
	wakeTokenQuery     = "?tenant=contoso&sig=tokensig"
	wakeClientSecret   = "client-secret-value"
	wakeTimedOutMsg    = "agent link wake call timed out"
	wakeNotAnsweredMsg = "agent link woke the agent and it has not connected"
)

type wakeLogText struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *wakeLogText) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *wakeLogText) all() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *wakeLogText) count(msg string) int {
	return strings.Count(w.all(), `msg="`+msg+`"`)
}

func captureAllLogs(t *testing.T) *wakeLogText {
	t.Helper()
	text := &wakeLogText{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(text, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return text
}

func requireNoSecretsLogged(t *testing.T, logged string) {
	t.Helper()
	for _, secret := range []string{"api-version", "code=shh", "tenant=contoso", "tokensig", wakeClientSecret} {
		if strings.Contains(logged, secret) {
			t.Fatalf("the log carries %q:\n%s", secret, logged)
		}
	}
}

func TestAWakeCallThatTimesOutIsLoggedAndTheNextRequestWakesTheAgentAgain(t *testing.T) {
	logs := captureAllLogs(t)
	hold := make(chan struct{})
	var wakes atomic.Int32
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		wakes.Add(1)
		select {
		case <-req.Context().Done():
		case <-hold:
		}
	}))
	t.Cleanup(runtimeSrv.Close)
	t.Cleanup(func() { close(hold) })
	r := newRig(t, map[string]any{"wake": map[string]any{"url": runtimeSrv.URL + "/wake" + wakeQuery}}, nil)
	r.adapter.agent(agentName).cfg.Wake.Timeout = 300 * time.Millisecond
	r.awaitWakeWatch()

	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return logs.count(wakeTimedOutMsg) == 1 })
	r.submit("T2", "tok:alice:T2", nil)
	waitFor(t, func() bool { return wakes.Load() == 2 })
	requireNoSecretsLogged(t, logs.all())
}

func TestAWakeCallThatTimesOutAfterTheAgentConnectedIsNotLogged(t *testing.T) {
	logs := captureAllLogs(t)
	var wakes atomic.Int32
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		wakes.Add(1)
		<-req.Context().Done()
	}))
	t.Cleanup(runtimeSrv.Close)
	r := newRig(t, map[string]any{"wake": map[string]any{"url": runtimeSrv.URL}}, nil)
	r.adapter.agent(agentName).cfg.Wake.Timeout = 600 * time.Millisecond
	r.awaitWakeWatch()

	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return wakes.Load() == 1 })
	r.consume(r.connect())
	time.Sleep(900 * time.Millisecond)
	if n := logs.count(wakeTimedOutMsg); n != 0 {
		t.Fatalf("a wake call the connected agent kept open was logged as timed out %d times:\n%s", n, logs.all())
	}
}

func TestAWakeThatIsAnsweredAndNeverFollowedByAConnectionIsLoggedOncePerClaim(t *testing.T) {
	logs := captureAllLogs(t)
	r, wakes := wakeRigAnswering(t, func(int32) int { return http.StatusAccepted })

	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return wakes.Load() == 1 })
	r.submit("T2", "tok:alice:T2", nil)
	waitFor(t, func() bool { return r.brk.watched.Load() == 2 })
	if n := logs.count(wakeNotAnsweredMsg); n != 0 {
		t.Fatalf("logged %d times before the wake interval passed", n)
	}

	r.locked(func(s *agentState) { s.lastWake = s.lastWake.Add(-2 * wakeInterval) })
	r.submit("T3", "tok:alice:T3", nil)
	waitFor(t, func() bool { return logs.count(wakeNotAnsweredMsg) == 1 })
	waitFor(t, func() bool { return wakes.Load() == 2 })
	r.submit("T4", "tok:alice:T4", nil)
	waitFor(t, func() bool { return r.brk.watched.Load() == 4 })
	if n := logs.count(wakeNotAnsweredMsg); n != 1 {
		t.Fatalf("logged %d times for one claim", n)
	}
}

func TestAFailingWakeLogsNeitherTheURLsQueryStringsNorTheClientSecret(t *testing.T) {
	tokenURL := func(srv *httptest.Server) string { return srv.URL + "/token" + wakeTokenQuery }
	wakeConfig := func(wakeURL, idpURL string) map[string]any {
		return map[string]any{"wake": map[string]any{
			"url": wakeURL, "token_url": idpURL, "client_id": "app", "client_secret": wakeClientSecret, "on_start": false,
		}}
	}
	t.Run("the wake call cannot connect", func(t *testing.T) {
		logs := captureAllLogs(t)
		idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":3600}`))
		}))
		t.Cleanup(idp.Close)
		gone := httptest.NewServer(http.NotFoundHandler())
		goneURL := gone.URL + "/wake" + wakeQuery
		gone.Close()
		r := newRig(t, wakeConfig(goneURL, tokenURL(idp)), nil)
		r.awaitWakeWatch()
		r.submit("T1", "tok:alice:T1", nil)
		waitFor(t, func() bool { return logs.count("agent link wake call failed") == 1 })
		requireNoSecretsLogged(t, logs.all())
	})
	t.Run("the token call cannot connect", func(t *testing.T) {
		logs := captureAllLogs(t)
		gone := httptest.NewServer(http.NotFoundHandler())
		goneTokenURL := tokenURL(gone)
		gone.Close()
		runtimeSrv := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(runtimeSrv.Close)
		r := newRig(t, wakeConfig(runtimeSrv.URL+"/wake"+wakeQuery, goneTokenURL), nil)
		r.awaitWakeWatch()
		r.submit("T1", "tok:alice:T1", nil)
		waitFor(t, func() bool { return logs.count("agent link wake token request failed") == 1 })
		requireNoSecretsLogged(t, logs.all())
	})
	t.Run("the wake call is refused", func(t *testing.T) {
		logs := captureAllLogs(t)
		idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":3600}`))
		}))
		t.Cleanup(idp.Close)
		runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(runtimeSrv.Close)
		r := newRig(t, wakeConfig(runtimeSrv.URL+"/wake"+wakeQuery, tokenURL(idp)), nil)
		r.awaitWakeWatch()
		r.submit("T1", "tok:alice:T1", nil)
		waitFor(t, func() bool { return logs.count("agent link wake call refused") == 1 })
		requireNoSecretsLogged(t, logs.all())
	})
}
