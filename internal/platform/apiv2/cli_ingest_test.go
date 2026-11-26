// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const cliIngestPath = "/productAnalytics/ingest/cli"

type recordingIngester struct {
	mu      sync.Mutex
	batches []platpkg.CLIIngestBatch
}

func (r *recordingIngester) Ingest(_ context.Context, batch platpkg.CLIIngestBatch) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, batch)
}

func (r *recordingIngester) received() []platpkg.CLIIngestBatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]platpkg.CLIIngestBatch(nil), r.batches...)
}

func newCLIIngestAPI(t *testing.T, ingester platpkg.CLIIngester) *entrypointAPI {
	t.Helper()
	mux := http.NewServeMux()
	register(mux, nil, false, cliIngestRoutes(ingester, slog.New(slog.DiscardHandler)))
	return &entrypointAPI{apiClient: &apiClient{t: t, handler: auth.DevMiddleware()(mux)}}
}

func cliIngestBody() map[string]any {
	return map[string]any{
		"platform": map[string]any{"os": "windows", "arch": "386", "appVersion": "main-v9.9.9"},
		"events": []map[string]any{
			{"eventId": "0192f3aa-0000-7000-8000-000000000001", "sessionId": "0192f3aa-0000-7000-8000-0000000000aa",
				"occurredAt": "2026-08-20T10:00:00.000Z", "event": "cli:command_run",
				"properties": map[string]any{"command": "sam config apply", "outcome": "success"}},
			{"eventId": "0192f3aa-0000-7000-8000-000000000002", "sessionId": "0192f3aa-0000-7000-8000-0000000000aa",
				"occurredAt": "2026-08-20T10:00:01.000Z", "event": "cli:command_run",
				"properties": map[string]any{"command": "sam skill list"}},
		},
	}
}

func TestCLIIngest_RelaysTheBatch(t *testing.T) {
	fake := &recordingIngester{}
	rec := newCLIIngestAPI(t, fake).call(http.MethodPut, cliIngestPath, cliIngestBody())
	expectStatus(t, rec, http.StatusNoContent)
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}

	want := platpkg.CLIIngestBatch{
		OS: "windows", Arch: "386", AppVersion: "main-v9.9.9",
		Events: []platpkg.CLIIngestBatchEvent{
			{EventID: "0192f3aa-0000-7000-8000-000000000001", SessionID: "0192f3aa-0000-7000-8000-0000000000aa",
				OccurredAt: "2026-08-20T10:00:00.000Z", Event: "cli:command_run",
				Properties: map[string]any{"command": "sam config apply", "outcome": "success"}},
			{EventID: "0192f3aa-0000-7000-8000-000000000002", SessionID: "0192f3aa-0000-7000-8000-0000000000aa",
				OccurredAt: "2026-08-20T10:00:01.000Z", Event: "cli:command_run",
				Properties: map[string]any{"command": "sam skill list"}},
		},
	}
	got := fake.received()
	if len(got) != 1 {
		t.Fatalf("ingester received %d batches, want 1", len(got))
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("batch = %+v, want %+v", got[0], want)
	}
}

func TestCLIIngest_NoRelayIs404(t *testing.T) {
	rec := newCLIIngestAPI(t, nil).call(http.MethodPut, cliIngestPath, cliIngestBody())
	expectStatus(t, rec, http.StatusNotFound)
	if body := decodeError(t, rec); body.Message != "CLI telemetry is not collected by this deployment" {
		t.Errorf("message = %q", body.Message)
	}
}

func TestCLIIngest_Validation(t *testing.T) {
	fake := &recordingIngester{}
	api := newCLIIngestAPI(t, fake)

	events := make([]map[string]any, 65)
	for i := range events {
		events[i] = map[string]any{"event": "cli:command_run"}
	}
	rec := api.call(http.MethodPut, cliIngestPath, map[string]any{"platform": map[string]any{}, "events": events})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if details := decodeError(t, rec).ValidationDetails; len(details["events"]) == 0 {
		t.Errorf("validationDetails has nothing for events: %v", details)
	}

	unnamed := api.call(http.MethodPut, cliIngestPath, map[string]any{"platform": map[string]any{}, "events": []map[string]any{{"event": ""}}})
	expectStatus(t, unnamed, http.StatusUnprocessableEntity)
	if details := decodeError(t, unnamed).ValidationDetails; len(details["event"]) == 0 {
		t.Errorf("validationDetails has nothing for event: %v", details)
	}

	expectStatus(t, api.call(http.MethodPut, cliIngestPath, `{"platform":{},"surface":"server","events":[]}`), http.StatusBadRequest)

	prefix, suffix := `{"platform":{"os":"`, `"},"events":[]}`
	oversized := prefix + strings.Repeat("a", cliIngestMaxBodyBytes+1-len(prefix)-len(suffix)) + suffix
	if len(oversized) != cliIngestMaxBodyBytes+1 {
		t.Fatalf("precondition: body is %d bytes, want %d", len(oversized), cliIngestMaxBodyBytes+1)
	}
	expectStatus(t, api.call(http.MethodPut, cliIngestPath, oversized), http.StatusRequestEntityTooLarge)

	if got := fake.received(); len(got) != 0 {
		t.Errorf("ingester received %d batches from refused requests, want none", len(got))
	}
}

func TestCLIIngest_Auth(t *testing.T) {
	core, _ := newTestCore(t)
	send := func(identity *auth.Subject) int {
		req := newJSONRequest(t, http.MethodPut, cliIngestPath, cliIngestBody())
		if identity != nil {
			req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))
		}
		return serveBare(t, core, req).Code
	}

	if got := send(nil); got != http.StatusUnauthorized {
		t.Errorf("no identity = %d, want 401", got)
	}
	if got := send(holdingKey("*")); got != http.StatusForbidden {
		t.Errorf("API key = %d, want 403", got)
	}
	admin := signedInSuperAdmin()
	noScopes := auth.NewTestSubject(auth.SubjectFields{
		UserID: admin.UserID(), Email: admin.Email(), Authenticated: true, AuthMethod: admin.AuthMethod(),
	})
	if got := send(noScopes); got == http.StatusUnauthorized || got == http.StatusForbidden {
		t.Errorf("signed in with no scopes = %d, want the gate to admit it", got)
	}
}
