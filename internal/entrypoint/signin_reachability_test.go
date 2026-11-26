// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestUnresolvedSenderTextKeepsTheFixableHalf(t *testing.T) {
	withURL := UnresolvedSenderText(SenderNotLinked, "https://sam.example.com")
	if !strings.Contains(withURL, "https://sam.example.com") {
		t.Errorf("with an address = %q, want it to carry the address", withURL)
	}

	withoutURL := UnresolvedSenderText(SenderNotLinked, "")
	if !strings.Contains(strings.ToLower(withoutURL), "signing in") {
		t.Errorf("without an address = %q, want it to still say signing in would link them", withoutURL)
	}
	if withoutURL == UnresolvedSenderText(SenderNotUsable, "") {
		t.Error("a fixable sender is told exactly what a stuck one is told; the two situations need different words")
	}

	stuck := UnresolvedSenderText(SenderNotUsable, "https://sam.example.com")
	if strings.Contains(strings.ToLower(stuck), "sign in") {
		t.Errorf("an unusable sender was told to sign in: %q", stuck)
	}
}

func TestWarnIfSignInUnreachable(t *testing.T) {
	warned := func(t *testing.T, baseURL string) bool {
		t.Helper()
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))
		core := &GatewayCore{cfg: &GatewayConfig{FrontendRedirectURL: baseURL}}
		core.WarnIfSignInUnreachable(logger, "whatsapp")

		for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("log line is not JSON: %v", err)
			}
			if lvl, _ := rec["level"].(string); lvl == "WARN" {
				if ep, _ := rec["entrypoint"].(string); ep != "whatsapp" {
					t.Errorf("entrypoint = %q, want it named so an operator knows which to fix", ep)
				}
				return true
			}
		}
		return false
	}

	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	if !warned(t, "") {
		t.Error("no warning with no address configured; the entrypoint would dead-end every unmatched sender in silence")
	}
	if warned(t, "https://sam.example.com/auth-callback.html") {
		t.Error("warned despite frontend_redirect_url resolving an address")
	}

	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	if warned(t, "") {
		t.Error("warned despite WEBUI_FRONTEND_SERVER_URL resolving an address")
	}
}

func TestWarnIfSignInUnreachableToleratesANilLogger(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	core := &GatewayCore{cfg: &GatewayConfig{}}
	core.WarnIfSignInUnreachable(nil, "whatsapp")
}
