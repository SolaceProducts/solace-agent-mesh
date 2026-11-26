// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestGraphReplySender_MasksRecipientOnEmptyBodySkip(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	g := &GraphReplySender{Logger: logger}
	if err := g.Send(context.Background(), ReplyRequest{To: "alice@corp.com", BodyText: "   "}); err != nil {
		t.Fatalf("empty-body skip should not error: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "skipping empty reply") {
		t.Fatalf("the skip log no longer fires; fixture is stale: %q", got)
	}
	if strings.Contains(got, "alice@corp.com") || strings.Contains(got, "alice") {
		t.Errorf("recipient address reached the log: %q", got)
	}
	if !strings.Contains(got, "***@corp.com") {
		t.Errorf("expected the domain-only handle maskEmail produces: %q", got)
	}
}
