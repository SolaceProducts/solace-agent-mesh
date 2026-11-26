// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestWithAuditCredential_ToleratesATypedNilIdentity(t *testing.T) {
	var identity *auth.Subject
	ctx := auth.ContextWithSubject(t.Context(), identity)
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	if got := withAuditCredential(ctx, logger); got == nil {
		t.Fatal("withAuditCredential returned nil")
	}
}
