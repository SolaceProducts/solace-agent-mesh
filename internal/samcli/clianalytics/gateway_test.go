// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package clianalytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

type ingestRecorder struct {
	status int

	mu      sync.Mutex
	calls   int
	path    string
	method  string
	auth    string
	surface string
	ctype   string
	body    gatewayIngestRequest
}

func (g *ingestRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.calls++
	g.path = r.URL.Path
	g.method = r.Method
	g.auth = r.Header.Get("Authorization")
	g.surface = r.Header.Get(httputil.HeaderClientSurface)
	g.ctype = r.Header.Get("Content-Type")
	_ = json.NewDecoder(r.Body).Decode(&g.body)
	g.mu.Unlock()

	status := g.status
	if status == 0 {
		status = http.StatusAccepted
	}
	w.WriteHeader(status)
}

func (g *ingestRecorder) hits() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func (g *ingestRecorder) snapshot() ingestRecorder {
	g.mu.Lock()
	defer g.mu.Unlock()
	return ingestRecorder{
		calls: g.calls, path: g.path, method: g.method,
		auth: g.auth, surface: g.surface, ctype: g.ctype, body: g.body,
	}
}

func newIngestGateway(t *testing.T, status int) (*ingestRecorder, Gateway) {
	t.Helper()
	rec := &ingestRecorder{status: status}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return rec, Gateway{BaseURL: srv.URL, Token: "gateway-token", AllowPlainHTTP: true}
}

func trackedCmd(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "sam"}
	child := TrackCommand(&cobra.Command{Use: "send"})
	root.AddCommand(child)
	return child
}

func TestAddressedGatewayReceivesTheRun(t *testing.T) {
	rec, gw := newIngestGateway(t, http.StatusAccepted)
	opts := testOptions(t, &captureSink{})
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	got := rec.snapshot()
	if got.calls != 1 {
		t.Fatalf("gateway received %d reports, want 1", got.calls)
	}
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/api/v1/productAnalytics/ingest/cli" {
		t.Errorf("path = %q, want /api/v1/productAnalytics/ingest/cli", got.path)
	}
	if got.auth != "Bearer gateway-token" {
		t.Errorf("authorization header = %q, want the bearer the command used", got.auth)
	}
	if got.surface != httputil.SurfaceCLI {
		t.Errorf("client surface header = %q, want %q", got.surface, httputil.SurfaceCLI)
	}
	if got.ctype != "application/json" {
		t.Errorf("content type = %q, want application/json", got.ctype)
	}
	if len(got.body.Events) != 1 {
		t.Fatalf("posted %d events, want 1", len(got.body.Events))
	}
	ev := got.body.Events[0]
	if ev.Event != productanalytics.EventCLICommandRun {
		t.Errorf("event = %q, want %q", ev.Event, productanalytics.EventCLICommandRun)
	}
	if ev.Properties[productanalytics.PropCommand] != "sam send" {
		t.Errorf("command = %v, want %q", ev.Properties[productanalytics.PropCommand], "sam send")
	}
	if ev.Properties[productanalytics.PropOutcome] != productanalytics.OutcomeSuccess {
		t.Errorf("outcome = %v, want %q", ev.Properties[productanalytics.PropOutcome], productanalytics.OutcomeSuccess)
	}
	if ev.SessionID == "" || ev.SessionID != sessionID() {
		t.Errorf("sessionId = %q, want this process's non-empty session", ev.SessionID)
	}
	if ev.OccurredAt == "" {
		t.Error("occurredAt is empty, want the CLI's own clock")
	}
}

func TestAddressedGatewayLeavesTheLocalStoreAlone(t *testing.T) {
	rec, gw := newIngestGateway(t, http.StatusAccepted)
	local := &captureSink{}
	opts := testOptions(t, local)
	grantConsent(t, opts)
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	if rec.hits() != 1 {
		t.Fatalf("gateway received %d reports, want 1", rec.hits())
	}
	if events := local.all(); len(events) != 0 {
		t.Fatalf("local sink delivered %d events, want none: the run belongs to the gateway's install", len(events))
	}
}

func TestAddressedGatewayReceivesTheRunEvenWithLocalConsentDenied(t *testing.T) {
	rec, gw := newIngestGateway(t, http.StatusAccepted)
	opts := testOptions(t, &captureSink{})
	denyConsent(t, opts)
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	if rec.hits() != 1 {
		t.Fatalf("gateway received %d reports, want 1", rec.hits())
	}
}

func TestUnaddressedRunStaysLocalAndCallsNothing(t *testing.T) {
	rec := &ingestRecorder{status: http.StatusAccepted}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	local := &captureSink{}
	opts := testOptions(t, local)
	grantConsent(t, opts)

	Report(t.Context(), trackedCmd(t), nil, opts)

	if rec.hits() != 0 {
		t.Fatalf("gateway received %d reports, want none for a local run", rec.hits())
	}
	if events := local.all(); len(events) != 1 {
		t.Fatalf("local sink delivered %d events, want 1", len(events))
	}
}

func TestDoNotTrackReportsNothingAnywhere(t *testing.T) {
	rec := &ingestRecorder{status: http.StatusAccepted}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	local := &captureSink{}
	opts := testOptions(t, local)
	t.Setenv("DO_NOT_TRACK", "1")
	grantConsent(t, opts)
	Report(t.Context(), trackedCmd(t), nil, opts)

	opts.Gateway = Gateway{BaseURL: srv.URL, Token: "gateway-token", AllowPlainHTTP: true}
	Report(t.Context(), trackedCmd(t), nil, opts)

	if rec.hits() != 0 {
		t.Fatalf("gateway received %d reports, want none under DO_NOT_TRACK", rec.hits())
	}
	if events := local.all(); len(events) != 0 {
		t.Fatalf("local sink delivered %d events, want none under DO_NOT_TRACK", len(events))
	}
}

func TestUnreachableGatewayReportsNothingAnywhere(t *testing.T) {
	rec := &ingestRecorder{}
	srv := httptest.NewServer(rec)
	dead := Gateway{BaseURL: srv.URL, Token: "gateway-token", AllowPlainHTTP: true}
	srv.Close()

	local := &captureSink{}
	opts := testOptions(t, local)
	grantConsent(t, opts)
	opts.Gateway = dead

	Report(t.Context(), trackedCmd(t), nil, opts)

	if events := local.all(); len(events) != 0 {
		t.Fatalf("local sink delivered %d events, want none: an unreachable gateway must not become a local report", len(events))
	}
}

func TestSilentGatewayIsAbandonedRatherThanWaitedOn(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-blocked
	}))
	t.Cleanup(func() {
		close(blocked)
		srv.Close()
	})

	local := &captureSink{}
	opts := testOptions(t, local)
	grantConsent(t, opts)
	opts.Gateway = Gateway{BaseURL: srv.URL, Token: "gateway-token", AllowPlainHTTP: true}

	done := make(chan time.Duration, 1)
	go func() {
		started := time.Now()
		Report(context.Background(), trackedCmd(t), nil, opts)
		done <- time.Since(started)
	}()

	const patience = 4 * gatewayReportTimeout
	select {
	case took := <-done:
		if took >= patience {
			t.Fatalf("Report took %s, want it abandoned near %s", took, gatewayReportTimeout)
		}
	case <-time.After(patience):
		t.Fatalf("Report did not return within %s: a silent gateway must be abandoned, not waited on", patience)
	}
	if events := local.all(); len(events) != 0 {
		t.Fatalf("local sink delivered %d events, want none", len(events))
	}
}

func TestRefusedGatewayRouteReportsNothingAnywhere(t *testing.T) {
	rec, gw := newIngestGateway(t, http.StatusNotFound)
	local := &captureSink{}
	opts := testOptions(t, local)
	grantConsent(t, opts)
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	if rec.hits() != 2 {
		t.Fatalf("deployment received %d reports, want the gateway route, then the platform's v2 one", rec.hits())
	}
	if events := local.all(); len(events) != 0 {
		t.Fatalf("local sink delivered %d events, want none", len(events))
	}
}

func TestPlainHTTPGatewayWithoutInsecureSendsNothing(t *testing.T) {
	rec := &ingestRecorder{status: http.StatusAccepted}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	opts := testOptions(t, &captureSink{})
	opts.Gateway = Gateway{BaseURL: srv.URL, Token: "gateway-token"}

	Report(t.Context(), trackedCmd(t), nil, opts)

	if rec.hits() != 0 {
		t.Fatalf("gateway received %d reports, want none: a bearer token must not go over plain http without --insecure", rec.hits())
	}
}

func TestAPIKeyIsNeverSentToTheGateway(t *testing.T) {
	rec, gw := newIngestGateway(t, http.StatusAccepted)
	gw.Token = "samk_not-a-real-key"
	local := &captureSink{}
	opts := testOptions(t, local)
	grantConsent(t, opts)
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	if rec.hits() != 0 {
		t.Fatalf("gateway received %d reports, want none: an api key cannot report a run", rec.hits())
	}
	if events := local.all(); len(events) != 0 {
		t.Fatalf("local sink delivered %d events, want none", len(events))
	}
}

func TestAnUnverifiedCertificateGetsTheTokenOnlyWhenTheCommandSkippedVerification(t *testing.T) {
	for name, tc := range map[string]struct {
		gw        Gateway
		wantCalls int
	}{
		"plain http allowed only": {Gateway{Token: "gateway-token", AllowPlainHTTP: true}, 0},
		"verification skipped":    {Gateway{Token: "gateway-token", AllowPlainHTTP: true, SkipTLSVerify: true}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &ingestRecorder{status: http.StatusAccepted}
			srv := httptest.NewTLSServer(rec)
			t.Cleanup(srv.Close)

			opts := testOptions(t, &captureSink{})
			opts.Gateway = tc.gw
			opts.Gateway.BaseURL = srv.URL

			Report(t.Context(), trackedCmd(t), nil, opts)

			if rec.hits() != tc.wantCalls {
				t.Fatalf("gateway received %d reports, want %d", rec.hits(), tc.wantCalls)
			}
		})
	}
}

func TestPlainHTTPGatewayWithoutATokenStillReports(t *testing.T) {
	rec := &ingestRecorder{status: http.StatusAccepted}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	opts := testOptions(t, &captureSink{})
	opts.Gateway = Gateway{BaseURL: srv.URL}

	Report(t.Context(), trackedCmd(t), nil, opts)

	got := rec.snapshot()
	if got.calls != 1 {
		t.Fatalf("gateway received %d reports, want 1", got.calls)
	}
	if got.auth != "" {
		t.Errorf("authorization header = %q, want none", got.auth)
	}
}

func TestGatewayIsNotCalledWhenTheFeatureIsOff(t *testing.T) {
	forceProductAnalyticsUnavailable(t)
	rec, gw := newIngestGateway(t, http.StatusAccepted)
	opts := testOptions(t, &captureSink{})
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	if rec.hits() != 0 {
		t.Fatalf("gateway received %d reports, want none with no collection path", rec.hits())
	}
}

func TestGatewayPathHonoursTheRunSuppressions(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  func(t *testing.T) *cobra.Command
	}{
		{"bare root", func(t *testing.T) *cobra.Command {
			t.Helper()
			root := TrackCommand(&cobra.Command{Use: "sam"})
			root.AddCommand(&cobra.Command{Use: "send"})
			return root
		}},
		{"help only", func(t *testing.T) *cobra.Command {
			t.Helper()
			cmd := trackedCmd(t)
			cmd.InitDefaultHelpFlag()
			if err := cmd.Flags().Set("help", "true"); err != nil {
				t.Fatalf("set help: %v", err)
			}
			return cmd
		}},
		{"untracked", func(t *testing.T) *cobra.Command {
			t.Helper()
			return &cobra.Command{Use: "send"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, gw := newIngestGateway(t, http.StatusAccepted)
			opts := testOptions(t, &captureSink{})
			opts.Gateway = gw

			Report(t.Context(), tc.cmd(t), nil, opts)

			if rec.hits() != 0 {
				t.Fatalf("gateway received %d reports, want none", rec.hits())
			}
		})
	}
}

func TestAddressedGatewayReceivesAFailedRun(t *testing.T) {
	rec, gw := newIngestGateway(t, http.StatusAccepted)
	opts := testOptions(t, &captureSink{})
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), context.Canceled, opts)

	got := rec.snapshot()
	if len(got.body.Events) != 1 {
		t.Fatalf("posted %d events, want 1", len(got.body.Events))
	}
	if outcome := got.body.Events[0].Properties[productanalytics.PropOutcome]; outcome != productanalytics.OutcomeError {
		t.Errorf("outcome = %v, want %q", outcome, productanalytics.OutcomeError)
	}
}

func TestGatewayIngestURLDoesNotDoubleTheSeparator(t *testing.T) {
	for _, base := range []string{"https://sam.example.com", "https://sam.example.com/"} {
		if got := (Gateway{BaseURL: base}).urlFor(gatewayIngestPath); got != "https://sam.example.com"+gatewayIngestPath {
			t.Errorf("urlFor(%q) = %q", base, got)
		}
	}
}

func TestAddressedGatewayReceivesThisHostPlatform(t *testing.T) {
	rec, gw := newIngestGateway(t, http.StatusAccepted)
	opts := testOptions(t, &captureSink{})
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	got := rec.snapshot()
	if got.calls != 1 {
		t.Fatalf("gateway received %d reports, want 1", got.calls)
	}
	if got.body.Platform.OS != runtime.GOOS {
		t.Errorf("platform.os = %q, want this host's %q", got.body.Platform.OS, runtime.GOOS)
	}
	if got.body.Platform.Arch != runtime.GOARCH {
		t.Errorf("platform.arch = %q, want this host's %q", got.body.Platform.Arch, runtime.GOARCH)
	}
	if got.body.Platform.AppVersion != samversion.Version {
		t.Errorf("platform.appVersion = %q, want this build's %q", got.body.Platform.AppVersion, samversion.Version)
	}
}

func TestGatewayIngestBodyMatchesTheRouteContract(t *testing.T) {
	body, err := json.Marshal(gatewayIngestRequest{Platform: localPlatform()})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, "request", decoded, "platform", "events")

	var platform map[string]json.RawMessage
	if err := json.Unmarshal(decoded["platform"], &platform); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, "platform", platform, "os", "arch", "appVersion")
}

func assertKeys(t *testing.T, what string, got map[string]json.RawMessage, want ...string) {
	t.Helper()
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sort.Strings(want)
	if !slices.Equal(keys, want) {
		t.Errorf("%s keys = %v, want %v", what, keys, want)
	}
}

func TestAddressedReportsWhetherAGatewayOwnsTheRun(t *testing.T) {
	if (Gateway{}).Addressed() {
		t.Error("the zero Gateway must not claim the run")
	}
	if !(Gateway{BaseURL: "https://sam.example.com"}).Addressed() {
		t.Error("a named gateway owns the run")
	}
}
