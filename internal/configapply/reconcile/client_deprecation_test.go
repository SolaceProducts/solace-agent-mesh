// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func TestClientRecordsTheDeprecatedRoutesThePlatformAnnounces(t *testing.T) {
	const notice = "https://docs.example.com/deprecations/version"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Solace-Api-Deprecated", notice)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	httputil.CLINotices.Reset()
	t.Cleanup(httputil.CLINotices.Reset)

	if _, err := NewClient(target.Resolved{URL: srv.URL, Token: "tok"}).FetchVersion(context.Background()); err != nil {
		t.Fatalf("FetchVersion: %v", err)
	}

	got := httputil.CLINotices.Message()
	if !strings.Contains(got, notice) {
		t.Fatalf("Message() = %q, want it to name %s", got, notice)
	}
}
