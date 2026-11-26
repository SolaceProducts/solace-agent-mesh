// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/google/uuid"
)

func TestConvertMessage_GeneratesMessageIDWhenOmitted(t *testing.T) {
	messageID := idgen.New()
	m := convertMessage(MessageDTO{Role: "user"}, "task-1", "ctx-1", messageID)
	parsed, err := uuid.Parse(m.MessageID)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("MessageID = %q, want a v7 UUID (err=%v)", m.MessageID, err)
	}
}

func TestConvertMessage_PreservesSuppliedMessageID(t *testing.T) {
	const supplied = "client-supplied-id"
	m := convertMessage(MessageDTO{Role: "user", MessageID: supplied}, "task-1", "ctx-1", supplied)
	if m.MessageID != supplied {
		t.Errorf("MessageID = %q, want the client value %q", m.MessageID, supplied)
	}
}
