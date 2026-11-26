// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func (r *webhookRig) refuse(path, body string, headers map[string]string) string {
	r.t.Helper()
	route := r.adapter.cfg.Routes[0]
	req := httptest.NewRequest(http.MethodPost, "/gw/"+testGW+path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return r.adapter.serve(route, httptest.NewRecorder(), req)
}

func TestWebhookRefusalsReportTheirReason(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		headers map[string]string
		reason  string
	}{
		{"a bad token is an authorization refusal", `{"message":"hi"}`, bearer("wrong-token"), taxonomy.RejectForbidden},
		{"a missing token is the same answer to the same question", `{"message":"hi"}`, nil, taxonomy.RejectForbidden},
		{"an input expression resolving to nothing is malformed caller data", `{"nothing":"here"}`, bearer("dev-webhook-token"), taxonomy.RejectMalformed},
		{"an unparseable body is malformed", `not json at all`, bearer("dev-webhook-token"), taxonomy.RejectMalformed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
			if got := rig.refuse("/token", c.body, c.headers); got != c.reason {
				t.Errorf("refusal reason = %q, want %q", got, c.reason)
			}
		})
	}
}

func TestWebhookRefusalReasonsAreInTheClosedSet(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	for _, body := range []string{`{"message":"hi"}`, `{"nothing":"here"}`, `not json`} {
		for _, headers := range []map[string]string{nil, bearer("wrong"), bearer("dev-webhook-token")} {
			reason := rig.refuse("/token", body, headers)
			if reason == "" {
				continue
			}
			if !slices.Contains(taxonomy.RejectReasons, reason) {
				t.Errorf("refusal reason %q is outside the closed set", reason)
			}
		}
	}
}

func TestAWebhookRefusalYieldsOneReason(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	if got := rig.refuse("/token", `{"nothing":"here"}`, bearer("wrong-token")); got != taxonomy.RejectForbidden {
		t.Errorf("refusal reason = %q, want the answering gate's %q alone", got, taxonomy.RejectForbidden)
	}
}

func TestAnAcceptedWebhookReportsNoRefusal(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	if got := rig.refuse("/token", `{"message":"hello"}`, bearer("dev-webhook-token")); got != "" {
		t.Fatalf("an accepted webhook reported refusal %q", got)
	}
}
