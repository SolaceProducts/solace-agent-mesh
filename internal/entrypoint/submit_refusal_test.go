// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestLogSubmitFailure_AnAgentThatIsNotDeployedLogsAtDebug(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	LogSubmitFailure(logger, slog.LevelWarn, fmt.Errorf("submit: %w", ErrAgentNotAvailable), "target", "a")

	if out := buf.String(); !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "its agent is not deployed") {
		t.Errorf("log = %q, want one debug line saying the agent is not deployed", out)
	}
}
