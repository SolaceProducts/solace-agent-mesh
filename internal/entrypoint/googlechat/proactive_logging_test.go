// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestHandOverFiles_ARefusalNamesNoEmail(t *testing.T) {
	rig := newProactiveRig(t)
	logs := &fullLog{}
	rig.a.logger = slog.New(logs)
	d := a2a.NotifyDeliver{UserID: notifyEmail, Artifacts: []a2a.NotifyArtifact{{AgentName: "a", UserID: "bob@example.com", SessionID: "s", Filename: "r.csv"}}}

	rig.a.handOverFiles(d, proactiveAddress{Space: notifySpace, Email: notifyEmail})

	if len(logs.lines) == 0 {
		t.Fatal("nothing was logged")
	}
	for _, line := range logs.lines {
		if strings.Contains(line, notifyEmail) || strings.Contains(line, "bob@example.com") {
			t.Errorf("a log line carries an email: %q", line)
		}
	}
}
