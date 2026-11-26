// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
)

const deprecationNotice = "https://docs.example.com/deprecations/old-route"

func TestExecutePrintsOneWarningForEveryDeprecatedRouteTheRunCalled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Solace-Api-Deprecated", deprecationNotice)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	root := &cobra.Command{Use: "sam", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(&cobra.Command{
		Use: "call",
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, path := range []string{"/api/v2/platform/a", "/api/v2/platform/b", "/api/v2/platform/a"} {
				opts := &apiOpts{
					stdout:      &bytes.Buffer{},
					stderr:      &bytes.Buffer{},
					stdin:       &bytes.Buffer{},
					positional:  []string{path},
					httpClient:  srv.Client(),
					insecure:    true,
					resolveBase: func(context.Context, *apiOpts) (string, error) { return srv.URL, nil },
					resolveTok:  func(context.Context, string, string) string { return "" },
				}
				if _, err := runAPI(cmd.Context(), opts); err != nil {
					return err
				}
			}
			return nil
		},
	})
	stderr := &bytes.Buffer{}
	root.SetOut(&bytes.Buffer{})
	root.SetErr(stderr)
	root.SetArgs([]string{"call"})

	if err := executeTree(root, clianalytics.Options{}); err != nil {
		t.Fatalf("executeTree: %v", err)
	}

	got := stderr.String()
	if strings.Count(got, "warning:") != 1 || strings.Count(got, deprecationNotice) != 1 {
		t.Fatalf("want exactly one warning naming the notice once, got %q", got)
	}
	for _, route := range []string{"GET /api/v2/platform/a", "GET /api/v2/platform/b"} {
		if !strings.Contains(got, route) {
			t.Errorf("warning should name %q, got %q", route, got)
		}
	}
}

func TestExecuteIsSilentWhenNoRouteWasDeprecated(t *testing.T) {
	httputil.CLINotices.Reset()
	root := &cobra.Command{Use: "sam", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(&cobra.Command{Use: "noop", RunE: func(*cobra.Command, []string) error { return nil }})
	stderr := &bytes.Buffer{}
	root.SetOut(&bytes.Buffer{})
	root.SetErr(stderr)
	root.SetArgs([]string{"noop"})

	if err := executeTree(root, clianalytics.Options{}); err != nil {
		t.Fatalf("executeTree: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
