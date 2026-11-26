// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
)

type arrived struct {
	surface string
	session string
}

func recordingPlatform(t *testing.T) (*Client, map[string]arrived) {
	t.Helper()
	seen := make(map[string]arrived)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = arrived{
			surface: httputil.ClientSurface(r),
			session: httputil.ClientSession(r),
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/platform/skills" {
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL, Token: "tok"}), seen
}

func TestEveryRequestDeclaresTheCLISurfaceAndSession(t *testing.T) {
	payload := []byte("a bundle small enough to go in one request")

	cases := []struct {
		name string
		path string
		call func(context.Context, *Client) error
	}{
		{
			name: "the JSON transport behind every typed call",
			path: "/api/v2/platform/agents",
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ListAgents(ctx)
				return err
			},
		},
		{
			name: "a toolset bundle uploaded in one request",
			path: "/api/v2/platform/toolsets/ts-1/bundle",
			call: func(ctx context.Context, c *Client) error {
				return c.UploadToolset(ctx, "ts-1", "bundle.zip", payload)
			},
		},
		{
			name: "a chunk of a chunked bundle upload",
			path: "/api/v2/platform/toolsetUploads/up-1/chunks/0",
			call: func(ctx context.Context, c *Client) error {
				return c.uploadChunk(ctx, "up-1", 0, payload)
			},
		},
		{
			name: "a toolset bundle downloaded for a pull",
			path: "/api/v2/platform/toolsets/ts-1/bundle",
			call: func(ctx context.Context, c *Client) error {
				_, err := c.DownloadToolset(ctx, "ts-1")
				return err
			},
		},
		{
			name: "a skill created with its bundle",
			path: "/api/v2/platform/skills",
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateSkill(ctx, "a skill", "skill.zip", payload)
				return err
			},
		},
		{
			name: "a skill bundle uploaded to an existing skill",
			path: "/api/v2/platform/skills/sk-1/bundle",
			call: func(ctx context.Context, c *Client) error {
				return c.UploadSkill(ctx, "sk-1", "skill.zip", payload)
			},
		},
		{
			name: "a skill bundle downloaded for a pull",
			path: "/api/v2/platform/skills/sk-1/bundle",
			call: func(ctx context.Context, c *Client) error {
				_, err := c.DownloadSkill(ctx, "sk-1")
				return err
			},
		},
		{
			name: "a connector spec uploaded",
			path: "/api/v2/platform/connectors/cn-1/resource",
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UploadConnectorResource(ctx, "cn-1", "spec.yaml", payload)
				return err
			},
		},
		{
			name: "a connector spec downloaded for a pull",
			path: "/api/v2/platform/connectors/cn-1/resource",
			call: func(ctx context.Context, c *Client) error {
				_, _, err := c.DownloadConnectorResource(ctx, "cn-1")
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, seen := recordingPlatform(t)
			if err := tc.call(t.Context(), c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got, ok := seen[tc.path]
			if !ok {
				t.Fatalf("no request arrived at %s; recorded %v", tc.path, seen)
			}
			if got.surface != httputil.SurfaceCLI {
				t.Errorf("surface = %q, want %q — the platform files this as a third-party REST call",
					got.surface, httputil.SurfaceCLI)
			}
			if got.session != clianalytics.SessionID() {
				t.Errorf("session = %q, want this process's %q", got.session, clianalytics.SessionID())
			}
		})
	}
}

func TestEveryRequestSiteUsesTheSharedHeaderSeam(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	built, declared := 0, 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		built += strings.Count(string(src), "http.NewRequestWithContext(")
		declared += strings.Count(string(src), "c.setClientHeaders(req)")
	}
	if built == 0 {
		t.Fatal("found no request sites; this test is no longer reading the package")
	}
	if built != declared {
		t.Errorf("%d request sites but %d call setClientHeaders — a site that declares no surface is read as a third-party REST call",
			built, declared)
	}
}
