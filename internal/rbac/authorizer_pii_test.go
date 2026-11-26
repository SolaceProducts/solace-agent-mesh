// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

func TestAuthorizerDenialLogsFingerprintIdentity(t *testing.T) {
	const email = "Person.Name@example.com"
	const phone = "whatsapp:15551234567"

	for _, tt := range []struct {
		name   string
		userID string
		deny   func(t *testing.T, logger *slog.Logger, identity *auth.Identity)
	}{
		{
			name:   "AuthorizeTool",
			userID: email,
			deny: func(t *testing.T, logger *slog.Logger, identity *auth.Identity) {
				if err := NewToolAuth(nil, logger).AuthorizeTool(context.Background(), identity, "some-tool", []string{"tool:file:update"}); err == nil {
					t.Fatal("expected a denial")
				}
			},
		},
		{
			name:   "FilterTools",
			userID: email,
			deny: func(t *testing.T, logger *slog.Logger, identity *auth.Identity) {
				NewToolAuth(nil, logger).FilterTools(context.Background(), identity,
					[]auth.ToolInfo{{Name: "file-writer", RequiredScopes: []string{"tool:file:update"}}})
			},
		},
		{
			name:   "AuthorizeAgent",
			userID: phone,
			deny: func(t *testing.T, logger *slog.Logger, identity *auth.Identity) {
				if err := NewAgentAuth(nil, logger).AuthorizeAgent(context.Background(), identity, "SomeAgent"); err == nil {
					t.Fatal("expected a denial")
				}
			},
		},
		{
			name:   "AuthorizeAgentScopes",
			userID: phone,
			deny: func(t *testing.T, logger *slog.Logger, identity *auth.Identity) {
				if err := NewAgentAuth(nil, logger).AuthorizeAgentScopes(context.Background(), identity, []string{"agent:SomeAgent:invoke"}); err == nil {
					t.Fatal("expected a denial")
				}
			},
		},
		{
			name:   "AuthorizeWorkflow",
			userID: email,
			deny: func(t *testing.T, logger *slog.Logger, identity *auth.Identity) {
				if err := NewAgentAuth(nil, logger).AuthorizeWorkflow(context.Background(), identity, "SomeWorkflow"); err == nil {
					t.Fatal("expected a denial")
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			tt.deny(t, logger, &auth.Identity{UserID: tt.userID, Scopes: []string{"tool:nothing:read"}})

			out := buf.String()
			if out == "" {
				t.Fatal("expected a denial log line")
			}
			if strings.Contains(out, tt.userID) {
				t.Errorf("denial log leaks the raw identity %q: %s", tt.userID, out)
			}
			if !strings.Contains(out, "userFP="+strutil.PIIFingerprint(tt.userID)) {
				t.Errorf("denial log must carry userFP=%s, got: %s", strutil.PIIFingerprint(tt.userID), out)
			}
			if strings.Contains(out, "userID=") {
				t.Errorf("denial log still uses the userID key: %s", out)
			}
		})
	}
}
