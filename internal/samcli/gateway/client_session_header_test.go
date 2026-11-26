// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

import (
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func TestSetHeadersDeclaresTheAnalyticsSession(t *testing.T) {
	c := &Client{Token: "t"}
	req := httptest.NewRequest("POST", "http://example.test/api/v1/message:stream", nil)
	c.setHeaders(req)

	if got := req.Header.Get(httputil.HeaderClientSession); got != clianalytics.SessionID() {
		t.Errorf("session header = %q, want this process's %q", got, clianalytics.SessionID())
	}
	if got := req.Header.Get(httputil.HeaderClientSurface); got != httputil.SurfaceCLI {
		t.Errorf("surface header = %q, want %q", got, httputil.SurfaceCLI)
	}
	if got := req.Header.Get(httputil.HeaderAPIVersion); got != samversion.APIVersion {
		t.Errorf("SAM-Version header = %q, want %q", got, samversion.APIVersion)
	}
}
