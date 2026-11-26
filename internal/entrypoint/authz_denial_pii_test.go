// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

func TestAuthorizeAgentDelegation_DenialLogFingerprintsIdentity(t *testing.T) {
	for _, userID := range []string{"whatsapp:15551234567", "Person.Name@example.com"} {
		t.Run(userID, func(t *testing.T) {
			core := newCanDelegateCore(t, "default_rbac")
			var buf bytes.Buffer
			core.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

			caller := auth.NewTestSubject(auth.SubjectFields{UserID: userID})
			ctx := auth.ContextWithSubject(context.Background(), caller)
			if err := core.authorizeAgentDelegation(ctx, caller, "some-agent"); err == nil {
				t.Fatal("expected a denial for a scope-less identity under enforcement")
			}

			out := buf.String()
			if !strings.Contains(out, "agent delegation denied") {
				t.Fatalf("expected the denial log line, got: %s", out)
			}
			if strings.Contains(out, userID) {
				t.Errorf("denial log leaks the raw identity %q: %s", userID, out)
			}
			if !strings.Contains(out, "userFP="+strutil.PIIFingerprint(userID)) {
				t.Errorf("denial log must carry userFP=%s, got: %s", strutil.PIIFingerprint(userID), out)
			}
		})
	}
}
