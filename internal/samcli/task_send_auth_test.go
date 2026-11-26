// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/gateway"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
)

func TestHandleAuthRequest(t *testing.T) {
	const url = "https://provider.example/authorize?state=xyz&code_challenge=c"

	t.Run("nil_request_is_noop", func(t *testing.T) {
		launches := installFakeBrowser(t)
		handleAuthRequest(nil, true, false)
		if got := *launches; len(got) != 0 {
			t.Fatalf("expected no browser launch, got %v", got)
		}
	})

	t.Run("empty_auth_uri_is_noop", func(t *testing.T) {
		launches := installFakeBrowser(t)
		handleAuthRequest(&gateway.AuthRequest{AuthURI: ""}, true, false)
		if got := *launches; len(got) != 0 {
			t.Fatalf("expected no browser launch, got %v", got)
		}
	})

	t.Run("open_browser_false_does_not_launch", func(t *testing.T) {
		launches := installFakeBrowser(t)
		handleAuthRequest(&gateway.AuthRequest{AuthURI: url}, false, false)
		if got := *launches; len(got) != 0 {
			t.Fatalf("expected no browser launch with --open-browser=false, got %v", got)
		}
	})

	t.Run("open_browser_true_launches_with_signal_uri", func(t *testing.T) {
		launches := installFakeBrowser(t)
		handleAuthRequest(&gateway.AuthRequest{AuthURI: url, TargetAgent: "jira"}, true, false)
		got := *launches
		if len(got) != 1 || got[0] != url {
			t.Fatalf("expected one browser launch with %q, got %v", url, got)
		}
	})

	t.Run("launcher_error_is_non_fatal", func(t *testing.T) {
		oauth.OverrideBrowser(func(string) error { return fmt.Errorf("xdg-open not found") })
		t.Cleanup(func() { oauth.OverrideBrowser(nil) })
		handleAuthRequest(&gateway.AuthRequest{AuthURI: url}, true, true)
	})
}

func TestApplyAuthRequest(t *testing.T) {
	const url = "https://provider.example/authorize?state=xyz"

	t.Run("no_signal_returns_false_no_launch", func(t *testing.T) {
		launches := installFakeBrowser(t)
		a := gateway.NewMessageAssembler()
		if applyAuthRequest(a.Message(), a, true, false) {
			t.Fatal("applyAuthRequest should return false when no AuthRequest")
		}
		if got := *launches; len(got) != 0 {
			t.Fatalf("expected no browser launch, got %v", got)
		}
	})

	t.Run("signal_dispatches_and_clears", func(t *testing.T) {
		launches := installFakeBrowser(t)
		a := gateway.NewMessageAssembler()

		_, _ = a.ProcessEvent("status_update", map[string]any{
			"result": map[string]any{
				"kind":   "status-update",
				"taskId": "task-1",
				"status": map[string]any{
					"message": map[string]any{
						"parts": []any{
							map[string]any{
								"kind": "data",
								"data": map[string]any{
									"type":     "authentication_required",
									"auth_uri": url,
								},
							},
						},
					},
				},
			},
		})

		if !applyAuthRequest(a.Message(), a, true, false) {
			t.Fatal("applyAuthRequest should return true when AuthRequest is present")
		}
		if got := *launches; len(got) != 1 || got[0] != url {
			t.Fatalf("expected one browser launch with %q, got %v", url, got)
		}
		if a.Message().AuthRequest != nil {
			t.Fatal("assembler should have cleared AuthRequest after dispatch")
		}
		if applyAuthRequest(a.Message(), a, true, false) {
			t.Fatal("second applyAuthRequest call should be a no-op")
		}
		if got := *launches; len(got) != 1 {
			t.Fatalf("expected no second browser launch, got %v", got)
		}
	})
}

func installFakeBrowser(t *testing.T) *[]string {
	t.Helper()
	var launches []string
	oauth.OverrideBrowser(func(u string) error {
		launches = append(launches, u)
		return nil
	})
	t.Cleanup(func() { oauth.OverrideBrowser(nil) })
	return &launches
}
