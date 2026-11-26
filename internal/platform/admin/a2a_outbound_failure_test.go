// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func oauthCardRequest(tokenURL, cardURL string) map[string]any {
	return map[string]any{
		"agentCardUrl":       cardURL,
		"authenticationType": "oauth2",
		"authConfig": map[string]any{
			"oauth2TokenUrl":     tokenURL,
			"oauth2ClientId":     "client",
			"oauth2ClientSecret": "secret",
		},
	}
}

func TestA2AOutbound_TokenAndCardRequestsHaveSeparateBudgets(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	const step = 600 * time.Millisecond
	const timeout = time.Second
	endpoints := newSSRFEndpoints(t, nil, nil)
	endpoints.svc.outboundTimeout = timeout

	target := newInternalTarget(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(step)
		if r.URL.Path == "/token" {
			_, _ = io.WriteString(w, `{"access_token":"t"}`)
			return
		}
		_, _ = io.WriteString(w, validCardJSON)
	})
	res := endpoints.postCard(t, oauthCardRequest(target.srv.URL+"/token", target.srv.URL+"/card.json"))
	if res.status != http.StatusOK {
		t.Fatalf("got %d %s; two %s steps under a %s per-request timeout must both fit", res.status, res.body, step, timeout)
	}
}

func TestA2AOutbound_StalledTokenEndpointIsCutOff(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	endpoints := newSSRFEndpoints(t, nil, nil)
	endpoints.svc.outboundTimeout = 300 * time.Millisecond

	release := make(chan struct{})
	target := newInternalTarget(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	res := endpoints.postCard(t, oauthCardRequest(target.srv.URL+"/token", unusedPublicLookingURL))
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("took %s, token timeout did not apply", elapsed)
	}
	if got, want := res.message(t), "oauth2 token request failed: the request timed out"; res.status != http.StatusUnprocessableEntity || got != want {
		t.Fatalf("got %d %q, want 422 %q", res.status, got, want)
	}
}

func TestA2AOutbound_FailureTextNamesTheClassNotTheTransportDetail(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	closed := newInternalTarget(t, nil)
	closedURL := closed.srv.URL
	closed.srv.Close()
	endpoints := newSSRFEndpoints(t, nil, nil)

	tests := []struct {
		name    string
		url     string
		want    []string
		notWant []string
	}{
		{"connection refused", closedURL + "/card.json", []string{"failed to fetch agent card: the connection was refused"}, []string{"127.0.0.1", "dial tcp"}},
		{"unresolvable host", "http://nonexistent-host.invalid/card.json", []string{
			"failed to fetch agent card: the host could not be resolved",
			"failed to fetch agent card: the request timed out"}, []string{"lookup", "nonexistent-host"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := endpoints.postCardURL(t, tt.url)
			if res.status != http.StatusUnprocessableEntity || !slices.Contains(tt.want, res.message(t)) {
				t.Fatalf("got %d %q, want 422 one of %q", res.status, res.message(t), tt.want)
			}
			for _, s := range tt.notWant {
				if strings.Contains(res.message(t), s) {
					t.Errorf("message contains %q: %q", s, res.message(t))
				}
			}
		})
	}
}
