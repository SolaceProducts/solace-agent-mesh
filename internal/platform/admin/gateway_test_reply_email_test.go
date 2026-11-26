// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
)

func TestTestEmailReplyConnection_WrapperStampsStageOutbound(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)
	resp, err := svc.testEmailReplyConnection(t.Context(), map[string]any{
		"auth_mode": "basic",
		"smtp_host": "smtp.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected infra error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected failure when required fields missing")
	}
	if resp.Stage != "outbound" {
		t.Errorf("Stage = %q, want %q", resp.Stage, "outbound")
	}
}
