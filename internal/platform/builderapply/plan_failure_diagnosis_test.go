// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestHandleApply_PlanFailureLogsItsCause(t *testing.T) {
	svc := newTestService(t)

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	if err := svc.deps.DB.Close(); err != nil {
		t.Fatalf("close DB: %v", err)
	}

	body, err := json.Marshal(ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("probe_db", "postgres", fullSQLValues())},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rec := postBuild(t, svc, string(body), allBuildScopes)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want 500 — this test is about what a 500 records", rec.Code, rec.Body.String())
	}
	logged := logs.String()
	if !strings.Contains(logged, "errorId") {
		t.Fatalf("the failure was not logged at all:\n%s", logged)
	}
	if !strings.Contains(logged, "error=") {
		t.Errorf("the log line carries no cause:\n%s\n"+
			"Without it the errorId joins to a line that says only \"internal error\", and every distinct planning failure — a dropped database, a missing converter, the never-delete tripwire — looks identical to whoever is paged.", logged)
	}
}
