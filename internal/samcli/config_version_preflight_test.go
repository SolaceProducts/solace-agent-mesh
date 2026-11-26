// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func versionServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/version" || body == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withClientVersion(t *testing.T, v string) {
	t.Helper()
	orig := samversion.Version
	samversion.Version = v
	t.Cleanup(func() { samversion.Version = orig })
}

func TestPreflightVersionCheck_FatalBelowFloor(t *testing.T) {
	withClientVersion(t, "main-v2.0.0")
	srv := versionServer(t, `{"products":[{"id":"sam-gateway-go","version":"main-v2.65.8"}],"minClientVersion":"main-v2.60.0"}`)
	tg := target.Target{URL: srv.URL}

	var buf bytes.Buffer
	err := preflightVersionCheck(t.Context(), tg, nil, newDiagnostics(&buf, textFormat).warn, false)
	if err == nil {
		t.Fatal("expected fatal error for client below floor, got nil")
	}
	if !strings.Contains(err.Error(), "min v2.60.0") {
		t.Errorf("error = %q, want it to name the floor", err.Error())
	}
}

func TestPreflightVersionCheck_WarnMinorBehind(t *testing.T) {
	withClientVersion(t, "main-v2.64.0")
	srv := versionServer(t, `{"products":[{"id":"sam-gateway-go","version":"main-v2.65.8"}]}`)
	tg := target.Target{URL: srv.URL}

	var buf bytes.Buffer
	if err := preflightVersionCheck(t.Context(), tg, nil, newDiagnostics(&buf, textFormat).warn, false); err != nil {
		t.Fatalf("minor-behind should warn, not error: %v", err)
	}
	if !strings.Contains(buf.String(), "warning:") {
		t.Errorf("expected a warning on stderr, got %q", buf.String())
	}
}

func TestPreflightVersionCheck_SkipFlag(t *testing.T) {
	withClientVersion(t, "main-v1.0.0")
	srv := versionServer(t, `{"products":[{"id":"sam-gateway-go","version":"main-v3.0.0"}]}`)
	tg := target.Target{URL: srv.URL}

	var buf bytes.Buffer
	if err := preflightVersionCheck(t.Context(), tg, nil, newDiagnostics(&buf, textFormat).warn, true); err != nil {
		t.Fatalf("skip flag must bypass the check entirely: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("skip flag should emit nothing, got %q", buf.String())
	}
}

func TestPreflightVersionCheck_MissingEndpointWarns(t *testing.T) {
	withClientVersion(t, "main-v1.0.0")
	srv := versionServer(t, "")
	tg := target.Target{URL: srv.URL}

	var buf bytes.Buffer
	if err := preflightVersionCheck(t.Context(), tg, nil, newDiagnostics(&buf, textFormat).warn, false); err != nil {
		t.Fatalf("missing version endpoint must be non-fatal: %v", err)
	}
	if !strings.Contains(buf.String(), "warning: could not check CLI/platform version compatibility") {
		t.Errorf("expected an unchecked-compatibility warning, got %q", buf.String())
	}
}

func TestPreflightVersionCheck_EnvOptOut(t *testing.T) {
	withClientVersion(t, "main-v1.0.0")
	t.Setenv("SAM_SKIP_VERSION_CHECK", "1")
	srv := versionServer(t, `{"products":[{"id":"sam-gateway-go","version":"main-v3.0.0"}]}`)
	tg := target.Target{URL: srv.URL}

	var buf bytes.Buffer
	if err := preflightVersionCheck(t.Context(), tg, nil, newDiagnostics(&buf, textFormat).warn, false); err != nil {
		t.Fatalf("SAM_SKIP_VERSION_CHECK must bypass the check: %v", err)
	}
}

func TestConfigPull_AbortsOnVersionFloor(t *testing.T) {
	withClientVersion(t, "main-v2.0.0")
	srv := versionServer(t, `{"minClientVersion":"main-v2.60.0"}`)

	cmd := newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"config", "pull", "--output", t.TempDir(), "--url", srv.URL})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "older than the platform requires") {
		t.Fatalf("expected version-floor error, got %v", err)
	}
}
