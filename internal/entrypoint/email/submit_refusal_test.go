// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type countingRefusals struct {
	reasons []string
}

func (c *countingRefusals) ReportSubmitRefusal(reason string) {
	if reason == "" {
		return
	}
	c.reasons = append(c.reasons, reason)
}

type misconfigDispatcher struct{}

func (misconfigDispatcher) Dispatch(context.Context, *NormalizedEmail, *gwauth.Subject, InternalTrustResult) (bool, error) {
	return false, nil
}

type erroringDispatcher struct{}

func (erroringDispatcher) Dispatch(context.Context, *NormalizedEmail, *gwauth.Subject, InternalTrustResult) (bool, error) {
	return false, errDispatchFailed
}

var errDispatchFailed = errors.New("submit task")

func refusalWorker(t *testing.T, tune func(*Worker)) (*Worker, *countingRefusals) {
	t.Helper()
	counted := &countingRefusals{}
	w := &Worker{
		Provider:               NewFakeProvider(nil),
		Resolver:               NewEmailIdentityResolver(slog.Default(), true),
		Policy:                 &AuthPolicy{Mode: ModeLogOnly, Logger: slog.Default()},
		TrustedAuthservIDs:     []string{"protection.outlook.com"},
		MaxMessageSizeBytes:    25 * 1024 * 1024,
		MaxAttachmentSizeBytes: 10 * 1024 * 1024,
		Dispatcher:             &fakeDispatcher{},
		Refusals:               counted,
		Logger:                 slog.Default(),
	}
	if tune != nil {
		tune(w)
	}
	return w, counted
}

func TestEmailRefusalsReportTheirReason(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		tune    func(*Worker)
		reason  string
	}{
		{
			name:    "a message over the size cap is malformed",
			fixture: "synthetic/basic_text.eml",
			tune:    func(w *Worker) { w.MaxMessageSizeBytes = 1 },
			reason:  taxonomy.RejectMalformed,
		},
		{
			name:    "a sender outside the allowlist is an authorization refusal",
			fixture: "synthetic/basic_text.eml",
			tune:    func(w *Worker) { w.AllowedSenders = []string{"someone-else@vendor.com"} },
			reason:  taxonomy.RejectForbidden,
		},
		{
			name:    "a message the auth policy rejects is an authorization refusal",
			fixture: "synthetic/auth_fail_dmarc.eml",
			tune: func(w *Worker) {
				w.Policy = &AuthPolicy{Mode: ModeRequireDMARC, Logger: slog.Default()}
			},
			reason: taxonomy.RejectForbidden,
		},
		{
			name:    "a default agent no card matches is an agent that is not found",
			fixture: "synthetic/basic_text.eml",
			tune:    func(w *Worker) { w.Dispatcher = misconfigDispatcher{} },
			reason:  taxonomy.RejectAgentNotFound,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, counted := refusalWorker(t, c.tune)
			w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, c.fixture)})

			if len(counted.reasons) != 1 {
				t.Fatalf("one refused message reported %d refusals, want 1: %v", len(counted.reasons), counted.reasons)
			}
			if counted.reasons[0] != c.reason {
				t.Errorf("refusal reason = %q, want %q", counted.reasons[0], c.reason)
			}
			if !slices.Contains(taxonomy.RejectReasons, counted.reasons[0]) {
				t.Errorf("refusal reason %q is outside the closed set", counted.reasons[0])
			}
		})
	}
}

func TestAnAcceptedEmailReportsNoRefusal(t *testing.T) {
	w, counted := refusalWorker(t, nil)
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})
	if len(counted.reasons) != 0 {
		t.Fatalf("an accepted email reported %v", counted.reasons)
	}
}

func TestARetriedEmailReportsNoRefusal(t *testing.T) {
	w, counted := refusalWorker(t, func(w *Worker) { w.Dispatcher = &erroringDispatcher{} })
	if got := w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")}); got != HandleRetry {
		t.Fatalf("disposition = %v, want HandleRetry", got)
	}
	if len(counted.reasons) != 0 {
		t.Fatalf("a retried email reported %v", counted.reasons)
	}
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})
	if len(counted.reasons) != 0 {
		t.Fatalf("a re-fetched email reported %v: one message must not be counted once per poll", counted.reasons)
	}
}

func TestAnEmailRefusalIsReportedOnce(t *testing.T) {
	w, counted := refusalWorker(t, func(w *Worker) {
		w.MaxMessageSizeBytes = 1
		w.AllowedSenders = []string{"someone-else@vendor.com"}
		w.Policy = &AuthPolicy{Mode: ModeRequireDMARC, Logger: slog.Default()}
	})
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/auth_fail_dmarc.eml")})

	if len(counted.reasons) != 1 {
		t.Fatalf("a message three gates would have refused reported %d refusals, want 1: %v", len(counted.reasons), counted.reasons)
	}
	if counted.reasons[0] != taxonomy.RejectMalformed {
		t.Errorf("refusal reason = %q, want the first gate's %q", counted.reasons[0], taxonomy.RejectMalformed)
	}
}

func TestTheEmailAdapterDeclaresTheClientItsRefusalsReport(t *testing.T) {
	a := &EmailAdapter{}
	if got := a.RelayedSurface(); got != httputil.SurfaceEmail {
		t.Fatalf("RelayedSurface = %q, want %q", got, httputil.SurfaceEmail)
	}
	if !slices.Contains(httputil.SubmitterSurfaces, a.RelayedSurface()) {
		t.Errorf("submittedFrom does not accept %q, so a refused mail would name no client", a.RelayedSurface())
	}
}

func TestAWorkerWithNoReporterIsInert(t *testing.T) {
	w, _ := refusalWorker(t, func(w *Worker) {
		w.Refusals = nil
		w.MaxMessageSizeBytes = 1
	})
	if got := w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")}); got != HandleDropped {
		t.Errorf("disposition = %v, want HandleDropped", got)
	}
}
