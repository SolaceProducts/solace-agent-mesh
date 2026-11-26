// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func RunHTTPFlow(ctx context.Context, t *testing.T, steps []HTTPFlowStep, gatewayURL string, idps map[string]*MockOIDCProvider) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	for i, step := range steps {
		var stepErr error
		switch step.Step {
		case "login":
			stepErr = ExecuteLogin(ctx, t, client, step, gatewayURL, idps)
		case "request":
			stepErr = ExecuteRequest(ctx, t, client, step, gatewayURL)
		case "request_with_bearer":
			stepErr = ExecuteBearerRequest(ctx, t, client, step, gatewayURL, idps)
		default:
			t.Fatalf("step %d: unknown step %q", i, step.Step)
		}
		if stepErr != nil {
			t.Fatalf("step %d (%s): %v", i, step.Step, stepErr)
		}
	}
}

func AssertOIDCCalls(ctx context.Context, t *testing.T, asserts []OIDCCallAssert, idps map[string]*MockOIDCProvider) {
	t.Helper()
	_ = ctx
	for _, a := range asserts {
		idp, ok := idps[a.FakeIDP]
		if !ok {
			t.Errorf("assert_oidc_calls: unknown fake_idp %q", a.FakeIDP)
			continue
		}
		checkOIDCCounter(t, "authorize_count", a.AuthorizeCount, a.AuthorizeCountAtLeast, idp.AuthorizeCount(), a.FakeIDP)
		checkOIDCCounter(t, "token_count", a.TokenCount, a.TokenCountAtLeast, idp.TokenCount(), a.FakeIDP)
		checkOIDCCounter(t, "userinfo_count", a.UserinfoCount, a.UserinfoCountAtLeast, idp.UserinfoCount(), a.FakeIDP)
	}
}

func checkOIDCCounter(t *testing.T, name string, exact, atLeast *int, actual int, idp string) {
	t.Helper()
	if exact != nil && actual != *exact {
		t.Errorf("[%s] %s: got %d, want %d", idp, name, actual, *exact)
	}
	if atLeast != nil && actual < *atLeast {
		t.Errorf("[%s] %s: got %d, want >= %d", idp, name, actual, *atLeast)
	}
}

func ExecuteLogin(ctx context.Context, t *testing.T, client *http.Client, step HTTPFlowStep, gatewayURL string, idps map[string]*MockOIDCProvider) error {
	t.Helper()
	if step.FakeIDP == "" || step.AsUser == "" {
		return fmt.Errorf("login step requires fake_idp and as_user")
	}
	if _, ok := idps[step.FakeIDP]; !ok {
		return fmt.Errorf("login: unknown fake_idp %q", step.FakeIDP)
	}

	prevCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Path == "/authorize" {
			q := req.URL.Query()
			if q.Get("fake_user_sub") == "" {
				q.Set("fake_user_sub", step.AsUser)
				req.URL.RawQuery = q.Encode()
			}
		}
		if len(via) > 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
	defer func() { client.CheckRedirect = prevCheckRedirect }()

	loginReq, err := http.NewRequestWithContext(ctx, "GET", gatewayURL+"/api/v1/auth/login", nil)
	if err != nil {
		return fmt.Errorf("build login request: %w", err)
	}
	resp, err := client.Do(loginReq)
	if err != nil {
		return fmt.Errorf("GET /api/v1/auth/login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if step.Expect.FinalStatus != 0 && resp.StatusCode != step.Expect.FinalStatus {
		return fmt.Errorf("final status: got %d, want %d", resp.StatusCode, step.Expect.FinalStatus)
	}
	if step.Expect.SessionCookieSet {
		if client.Jar == nil {
			return fmt.Errorf("session_cookie_set: true requires a cookie jar on the http.Client")
		}
		u, err := url.Parse(gatewayURL)
		if err != nil {
			return fmt.Errorf("parse gatewayURL %q: %w", gatewayURL, err)
		}
		var found bool
		for _, c := range client.Jar.Cookies(u) {
			if c.Name == "sam_access_token" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("expected sam_access_token cookie to be set")
		}
	}
	return nil
}

func ExecuteRequest(ctx context.Context, t *testing.T, client *http.Client, step HTTPFlowStep, gatewayURL string) error {
	t.Helper()
	method := step.Method
	if method == "" {
		method = "GET"
	}
	req, err := http.NewRequestWithContext(ctx, method, gatewayURL+step.Path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	return doRequestAndAssert(t, client, req, step.Expect)
}

func ExecuteBearerRequest(ctx context.Context, t *testing.T, client *http.Client, step HTTPFlowStep, gatewayURL string, idps map[string]*MockOIDCProvider) error {
	t.Helper()
	if step.Bearer == nil {
		return fmt.Errorf("request_with_bearer requires bearer")
	}
	var token string
	switch {
	case step.Bearer.Literal != "":
		token = step.Bearer.Literal
	case step.Bearer.FromFakeIDP != nil:
		idp, ok := idps[step.Bearer.FromFakeIDP.Name]
		if !ok {
			return fmt.Errorf("unknown fake_idp %q", step.Bearer.FromFakeIDP.Name)
		}
		minted, err := idp.IssueAccessToken(step.Bearer.FromFakeIDP.Sub)
		if err != nil {
			return fmt.Errorf("mint bearer for %s: %w", step.Bearer.FromFakeIDP.Name, err)
		}
		token = minted
	default:
		return fmt.Errorf("bearer requires literal or from_fake_idp")
	}
	method := step.Method
	if method == "" {
		method = "GET"
	}
	req, err := http.NewRequestWithContext(ctx, method, gatewayURL+step.Path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return doRequestAndAssert(t, client, req, step.Expect)
}

func doRequestAndAssert(t *testing.T, client *http.Client, req *http.Request, expect HTTPFlowExpect) error {
	t.Helper()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("client.Do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if expect.Status != 0 && resp.StatusCode != expect.Status {
		return fmt.Errorf("status: got %d, want %d", resp.StatusCode, expect.Status)
	}
	if expect.BodyContains != "" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}
		if !strings.Contains(string(body), expect.BodyContains) {
			return fmt.Errorf("body does not contain %q: %s", expect.BodyContains, body)
		}
	}
	if expect.CookieName != "" {
		if client.Jar == nil {
			return fmt.Errorf("cookie_name %q requires a cookie jar on the http.Client", expect.CookieName)
		}
		var found bool
		for _, c := range client.Jar.Cookies(req.URL) {
			if c.Name == expect.CookieName {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("expected cookie %q to be set", expect.CookieName)
		}
	}
	return nil
}
