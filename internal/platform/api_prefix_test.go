// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleWebUIRoute_ServesTheV1AndInternalPrefixes(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	HandleWebUIRoute(mux, http.MethodGet, "/builder/sessions/{sessionId}/test-agent", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.PathValue("sessionId")))
	})

	for _, prefix := range []string{V1Prefix, InternalPrefix} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, prefix+"/builder/sessions/abc/test-agent", nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "abc" {
			t.Errorf("GET under %s = %d %q, want 200 \"abc\"", prefix, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, APIV2Prefix+"/builder/sessions/abc/test-agent", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET under the v2 prefix = %d, want 404", rec.Code)
	}
}

func TestNoAPIKeyRouteIsOnTheInternalPrefix(t *testing.T) {
	t.Parallel()

	for _, pattern := range APIKeyRoutePatterns() {
		if strings.Contains(pattern, InternalPrefix) {
			t.Errorf("%q is on the API key allowlist: the internal prefix is for the web UI alone", pattern)
		}
	}
}
