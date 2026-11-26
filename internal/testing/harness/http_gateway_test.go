// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestHTTPGatewaySatisfiesOAuthCapableGateway(t *testing.T) {
	var _ OAuthCapableGateway = (*HTTPGateway)(nil)
}

func TestHTTPGatewaySendAuthResponseHitsCallback(t *testing.T) {
	var gotCode, gotState string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCode = r.URL.Query().Get("code")
		gotState = r.URL.Query().Get("state")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>Authentication Successful</body></html>"))
	}))
	defer srv.Close()

	gw := NewHTTPGateway("go", "ns", "127.0.0.1:0", false, slog.Default())
	gw.baseURL = srv.URL

	signal := &a2a.AuthenticationRequiredData{
		AuthURI: srv.URL + "/auth?client_id=cid&code_challenge=abc&redirect_uri=http://x&state=st-xyz",
	}

	ctx := t.Context()

	if err := gw.SendAuthResponse(ctx, "test_agent", signal, "code-42", "st-xyz"); err != nil {
		t.Fatalf("SendAuthResponse returned error: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("callback method: got %q, want GET", gotMethod)
	}
	if gotCode != "code-42" {
		t.Errorf("callback code: got %q, want %q", gotCode, "code-42")
	}
	if gotState != "st-xyz" {
		t.Errorf("callback state: got %q, want %q", gotState, "st-xyz")
	}
}

func TestHTTPGatewaySendAuthResponseRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad code"))
	}))
	defer srv.Close()

	gw := NewHTTPGateway("go", "ns", "127.0.0.1:0", false, slog.Default())
	gw.baseURL = srv.URL

	signal := &a2a.AuthenticationRequiredData{
		AuthURI: srv.URL + "/auth?state=s1",
	}
	err := gw.SendAuthResponse(t.Context(), "test_agent", signal, "bad-code", "s1")
	if err == nil {
		t.Fatal("expected error when callback returns 400, got nil")
	}
}

func TestHTTPGatewaySendAuthResponseRejectsMissingSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>something else</html>"))
	}))
	defer srv.Close()

	gw := NewHTTPGateway("go", "ns", "127.0.0.1:0", false, slog.Default())
	gw.baseURL = srv.URL

	signal := &a2a.AuthenticationRequiredData{AuthURI: srv.URL + "/auth?state=s1"}
	err := gw.SendAuthResponse(t.Context(), "test_agent", signal, "code", "s1")
	if err == nil {
		t.Fatal("expected error when body lacks success marker")
	}
}

func TestHTTPGatewaySurfacesAuthRequiredSignal(t *testing.T) {
	logger := slog.Default()
	gw := NewHTTPGateway("go", "ns", "127.0.0.1:0", false, logger)

	ch := gw.AuthRequiredChan()
	if ch == nil {
		t.Fatal("AuthRequiredChan returned nil")
	}

	payload := json.RawMessage(`{"result":{"status":{"message":{"parts":[
      {"kind":"data","data":{
        "type":"authentication_required",
        "credential_key":"ck-1",
        "target_agent":"test_agent",
        "user_id":"u1",
        "client_id":"cid",
        "auth_uri":"https://mock/authorize?code_challenge=abc&redirect_uri=http%3A%2F%2Fx%2Fcb&client_id=cid&state=st-1",
        "code_verifier":"cv-1",
        "auth_request_function_call_id":"fc-1"
      }}
    ]}}}}`)

	gw.maybeSurfaceAuthSignal(payload)

	select {
	case got := <-ch:
		if got == nil || got.CredentialKey != "ck-1" {
			t.Fatalf("unexpected signal: %+v", got)
		}
	default:
		t.Fatal("AuthRequiredChan did not yield the signal")
	}
}

func TestHTTPGatewaySetsAndRestoresRedirectURI(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://prev.example/old")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	gw := NewHTTPGateway("go", "ns", "127.0.0.1:0", false, slog.Default())
	gw.baseURL = srv.URL

	gw.applyOAuthRedirectURI()

	want := srv.URL + "/api/v1/auth/tool/callback"
	if got := os.Getenv("OAUTH_TOOL_REDIRECT_URI"); got != want {
		t.Errorf("OAUTH_TOOL_REDIRECT_URI after Setup: got %q, want %q", got, want)
	}

	gw.restoreOAuthRedirectURI()

	if got := os.Getenv("OAUTH_TOOL_REDIRECT_URI"); got != "http://prev.example/old" {
		t.Errorf("OAUTH_TOOL_REDIRECT_URI after Cleanup: got %q, want restore to prev", got)
	}
}

func TestHTTPGatewaySkipsRedirectURIForPythonMode(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://prev.example/old")

	gw := NewHTTPGateway("python", "ns", "127.0.0.1:0", false, slog.Default())
	gw.baseURL = "http://ignore.example"

	gw.applyOAuthRedirectURI()

	if got := os.Getenv("OAUTH_TOOL_REDIRECT_URI"); got != "http://prev.example/old" {
		t.Errorf("python mode should NOT modify OAUTH_TOOL_REDIRECT_URI; got %q", got)
	}
}

func TestHTTPGatewayRedirectURISerializedAcrossGateways(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://initial")

	gw1 := NewHTTPGateway("go", "ns", "127.0.0.1:0", false, slog.Default())
	gw1.baseURL = "http://gw1.example"
	gw2 := NewHTTPGateway("go", "ns", "127.0.0.1:0", false, slog.Default())
	gw2.baseURL = "http://gw2.example"

	gw1.applyOAuthRedirectURI()
	t.Cleanup(gw1.restoreOAuthRedirectURI)

	if got := os.Getenv("OAUTH_TOOL_REDIRECT_URI"); got != "http://gw1.example/api/v1/auth/tool/callback" {
		t.Fatalf("after gw1 apply: got %q", got)
	}

	var gw2ApplyDone sync.WaitGroup
	done := make(chan struct{})
	gw2ApplyDone.Go(func() {
		gw2.applyOAuthRedirectURI()
		close(done)
	})
	t.Cleanup(func() {
		gw2ApplyDone.Wait()
		gw2.restoreOAuthRedirectURI()
	})

	select {
	case <-done:
		t.Fatal("gw2.applyOAuthRedirectURI did NOT block while gw1 held the mutex")
	case <-time.After(50 * time.Millisecond):
	}

	gw1.restoreOAuthRedirectURI()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("gw2.applyOAuthRedirectURI did not unblock after gw1 restored")
	}

	if got := os.Getenv("OAUTH_TOOL_REDIRECT_URI"); got != "http://gw2.example/api/v1/auth/tool/callback" {
		t.Fatalf("after gw2 apply: got %q", got)
	}

	gw2.restoreOAuthRedirectURI()
	if got := os.Getenv("OAUTH_TOOL_REDIRECT_URI"); got != "http://initial" {
		t.Fatalf("after gw2 restore: got %q, want initial value", got)
	}
}
