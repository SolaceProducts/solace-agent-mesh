// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestInboundOutcomeLogsByName(t *testing.T) {
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("held", "outcome", outcomeUnauthorized)
	if !strings.Contains(buf.String(), `"outcome":"unauthorized"`) {
		t.Errorf("log = %s, want the outcome by name", buf.String())
	}
}
