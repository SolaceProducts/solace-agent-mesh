// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const serverWithoutV2Manifest = "kind: manifest\nname: test\ntarget:\n  url: %s\n  auth:\n    type: bearer_token\n    env_var: SAM_PLATFORM_TOKEN\nresources:\n  agents: []\n"

func newServerWithoutV2(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			_, _ = w.Write([]byte(`{"version":"2.500.0"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCLIStopsWithUpgradeMessageWhenServerHasNoV2(t *testing.T) {
	srv := newServerWithoutV2(t)
	manifest := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(manifest, []byte(strings.Replace(serverWithoutV2Manifest, "%s", srv.URL, 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, args := range map[string][]string{
		"config plan": {"config", "plan", "-m", manifest, "--no-dotenv", "--no-interactive"},
		"eval list":   {"eval", "list", "--url", srv.URL, "--insecure"},
		"api-key":     {"api-key", "list", "--url", srv.URL, "--insecure"},
	} {
		t.Run(name, func(t *testing.T) {
			rerouteHome(t)
			isolateGatewayRecording(t)
			t.Setenv("SAM_PLATFORM_TOKEN", "token")
			t.Setenv("SAM_AUTH_TOKEN", "token")
			out, err := runSam(name, args...)
			text := out.text
			if err != nil {
				text += err.Error()
			}
			if err == nil {
				t.Fatalf("succeeded against a server without /api/v2:\n%s", text)
			}
			for _, want := range []string{"does not serve /api/v2", "upgrade Agent Mesh to"} {
				if !strings.Contains(text, want) {
					t.Errorf("output does not mention %q:\n%s", want, text)
				}
			}
		})
	}
}

func TestAPIExplainsA404FromAServerWithoutV2(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	h.opts.positional = []string{"/api/v2/platform/agents"}

	code, err := h.run()
	if err != nil || code != 1 {
		t.Fatalf("code, err = %d, %v; want 1, nil", code, err)
	}
	for _, want := range []string{"does not serve /api/v2", "upgrade Agent Mesh to"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr does not mention %q:\n%s", want, h.stderr)
		}
	}
}

func TestAPIDoesNotBlameTheServerForAMistypedV2Path(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/platform/health" {
			return
		}
		http.NotFound(w, r)
	}))
	h.opts.positional = []string{"/api/v2/platform/agentz"}

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.stderr.String(), "does not serve /api/v2") {
		t.Errorf("stderr blames the server:\n%s", h.stderr)
	}
}
