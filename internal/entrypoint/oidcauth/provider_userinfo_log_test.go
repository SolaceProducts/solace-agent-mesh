// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	userInfoTestAccessToken = "secret-access-token-xyz"
	userInfoTestQuerySecret = "query-secret-123"
	userInfoTestBodySecret  = "body-secret-alice@example.com"
)

type userInfoFailureLog struct {
	Level      string  `json:"level"`
	Msg        string  `json:"msg"`
	Flow       string  `json:"flow"`
	HTTPStatus float64 `json:"httpStatus"`
	Detail     string  `json:"detail"`
	raw        string
}

func userInfoFailureLogs(t *testing.T, buf *bytes.Buffer) []userInfoFailureLog {
	t.Helper()
	var out []userInfoFailureLog
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec userInfoFailureLog
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		if rec.Msg == "user info fetch failed" {
			rec.raw = line
			out = append(out, rec)
		}
	}
	return out
}

func newUserInfoIDP(t *testing.T, userinfo http.HandlerFunc) (*httptest.Server, *NativeOIDCClient) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 srvIssuer(r),
				"authorization_endpoint": srvIssuer(r) + "/authorize",
				"token_endpoint":         srvIssuer(r) + "/token",
				"userinfo_endpoint":      srvIssuer(r) + "/userinfo?apikey=" + userInfoTestQuerySecret,
				"jwks_uri":               srvIssuer(r) + "/jwks",
			})
			return
		}
		userinfo(w, r)
	}))
	t.Cleanup(srv.Close)

	client, err := NewNativeOIDCClient(OIDCConfig{Issuer: srv.URL, ClientID: "client-id"}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}
	if err := client.WarmDiscovery(t.Context()); err != nil {
		t.Fatalf("WarmDiscovery: %v", err)
	}
	return srv, client
}

func TestMintFromOIDCResponse_UserInfoFailureLogSaysWhy(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	for _, tc := range []struct {
		name       string
		handler    http.HandlerFunc
		prepare    func(srv *httptest.Server, client *NativeOIDCClient)
		wantStatus int
		wantDetail string
	}{
		{
			name: "idp answers 503",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprint(w, userInfoTestBodySecret)
			},
			wantStatus: http.StatusServiceUnavailable,
			wantDetail: "unexpected status",
		},
		{
			name: "idp does not answer in time",
			handler: func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-release:
				case <-r.Context().Done():
				}
			},
			prepare:    func(_ *httptest.Server, client *NativeOIDCClient) { client.httpClient.Timeout = 50 * time.Millisecond },
			wantDetail: "timeout",
		},
		{
			name:       "idp refuses the connection",
			handler:    func(http.ResponseWriter, *http.Request) {},
			prepare:    func(srv *httptest.Server, _ *NativeOIDCClient) { srv.Close() },
			wantDetail: "connection refused",
		},
		{
			name: "idp answers with a body that does not parse",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, userInfoTestBodySecret)
			},
			wantDetail: "invalid response",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, client := newUserInfoIDP(t, tc.handler)
			if tc.prepare != nil {
				tc.prepare(srv, client)
			}
			var buf bytes.Buffer
			p := newTestProvider(t, nil)
			p.oidcClient = client
			p.logger = slog.New(slog.NewJSONHandler(&buf, nil))

			_, _, _, fail := p.mintFromOIDCResponse(t.Context(), httptest.NewRecorder(),
				&TokenResponse{AccessToken: userInfoTestAccessToken}, "webui_login", "")
			if fail == nil || fail.reason != authReasonOIDCUserinfoFail {
				t.Fatalf("fail = %+v, want the userinfo failure", fail)
			}

			logs := userInfoFailureLogs(t, &buf)
			if len(logs) != 1 {
				t.Fatalf("got %d 'user info fetch failed' logs, want 1: %s", len(logs), buf.String())
			}
			got := logs[0]
			if got.Level != "ERROR" || got.Flow != "webui_login" {
				t.Errorf("level/flow = %q/%q, want ERROR/webui_login", got.Level, got.Flow)
			}
			if int(got.HTTPStatus) != tc.wantStatus {
				t.Errorf("httpStatus = %v, want %d", got.HTTPStatus, tc.wantStatus)
			}
			if got.Detail != tc.wantDetail {
				t.Errorf("detail = %q, want %q", got.Detail, tc.wantDetail)
			}
			for _, secret := range []string{userInfoTestAccessToken, userInfoTestQuerySecret, userInfoTestBodySecret} {
				if strings.Contains(got.raw, secret) {
					t.Errorf("log leaks %q: %s", secret, got.raw)
				}
			}
		})
	}
}

func TestMintFromOIDCResponse_UserInfoFailureLogForUnclassifiedError(t *testing.T) {
	var buf bytes.Buffer
	p := newTestProvider(t, &fakeOIDCClient{})
	p.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	_, _, _, fail := p.mintFromOIDCResponse(t.Context(), httptest.NewRecorder(),
		&TokenResponse{AccessToken: userInfoTestAccessToken}, "", "")
	if fail == nil {
		t.Fatal("expected the userinfo failure")
	}

	logs := userInfoFailureLogs(t, &buf)
	if len(logs) != 1 {
		t.Fatalf("got %d 'user info fetch failed' logs, want 1: %s", len(logs), buf.String())
	}
	if logs[0].Detail != "request failed" || logs[0].HTTPStatus != 0 {
		t.Errorf("detail/httpStatus = %q/%v, want \"request failed\"/0", logs[0].Detail, logs[0].HTTPStatus)
	}
}
