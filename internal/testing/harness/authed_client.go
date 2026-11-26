// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"testing"
)

func AuthedClient(ctx context.Context, t *testing.T, gatewayURL string, idp *MockOIDCProvider, sub string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	const idpKey = "authed-client-idp"
	step := HTTPFlowStep{
		Step:    "login",
		FakeIDP: idpKey,
		AsUser:  sub,
		Expect:  HTTPFlowExpect{SessionCookieSet: true},
	}
	if err := ExecuteLogin(ctx, t, client, step, gatewayURL, map[string]*MockOIDCProvider{idpKey: idp}); err != nil {
		t.Fatalf("AuthedClient: login as %q failed: %v", sub, err)
	}
	return client
}
