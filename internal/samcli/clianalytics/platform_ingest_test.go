// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package clianalytics

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

type originRecorder struct {
	serves map[ingestRoute]int

	mu       sync.Mutex
	attempts []string
	methods  []string
	accepted []gatewayIngestRequest
	surfaces []string
	auths    []string
}

func (o *originRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	o.attempts = append(o.attempts, r.URL.Path)
	o.methods = append(o.methods, r.Method)
	status, served := o.serves[ingestRoute{r.Method, r.URL.Path}]
	if served {
		var body gatewayIngestRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		o.accepted = append(o.accepted, body)
		o.surfaces = append(o.surfaces, r.Header.Get(httputil.HeaderClientSurface))
		o.auths = append(o.auths, r.Header.Get("Authorization"))
	}
	o.mu.Unlock()

	switch {
	case served:
		w.WriteHeader(status)
	case o.servesPath(r.URL.Path):
		w.WriteHeader(http.StatusMethodNotAllowed)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (o *originRecorder) servesPath(path string) bool {
	for route := range o.serves {
		if route.path == path {
			return true
		}
	}
	return false
}

func (o *originRecorder) attemptedMethods() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.methods...)
}

func (o *originRecorder) snapshot() ([]string, []gatewayIngestRequest, []string, []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.attempts...), append([]gatewayIngestRequest(nil), o.accepted...),
		append([]string(nil), o.surfaces...), append([]string(nil), o.auths...)
}

var (
	gatewayRoute    = ingestRoute{http.MethodPost, gatewayIngestPath}
	platformV2Route = ingestRoute{http.MethodPut, platformIngestPath}
	platformV1Route = ingestRoute{http.MethodPost, "/api/v1/platform/productAnalytics/ingest/cli"}
	acceptStatus    = map[ingestRoute]int{
		gatewayRoute:    http.StatusAccepted,
		platformV2Route: http.StatusNoContent,
		platformV1Route: http.StatusAccepted,
	}
)

func newOrigin(t *testing.T, routes ...ingestRoute) (*originRecorder, Gateway) {
	t.Helper()
	serves := make(map[ingestRoute]int, len(routes))
	for _, route := range routes {
		serves[route] = acceptStatus[route]
	}
	rec := &originRecorder{serves: serves}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return rec, Gateway{BaseURL: srv.URL, Token: "platform-token", AllowPlainHTTP: true}
}

func TestSplitPlatformOriginReceivesTheRun(t *testing.T) {
	rec, gw := newOrigin(t, platformV2Route)
	local := &captureSink{}
	opts := testOptions(t, local)
	grantConsent(t, opts)
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	attempts, accepted, surfaces, auths := rec.snapshot()
	want := []string{
		"/api/v1/productAnalytics/ingest/cli",
		"/api/v2/platform/productAnalytics/ingest/cli",
	}
	if !slices.Equal(attempts, want) {
		t.Fatalf("attempts = %v, want the gateway route, then the platform's one %v", attempts, want)
	}
	if len(accepted) != 1 {
		t.Fatalf("the platform route accepted %d reports, want 1", len(accepted))
	}
	if len(accepted[0].Events) != 1 || accepted[0].Events[0].Event != productanalytics.EventCLICommandRun {
		t.Fatalf("posted %+v, want one cli:command_run", accepted[0].Events)
	}
	if got := accepted[0].Events[0].Properties[productanalytics.PropCommand]; got != "sam send" {
		t.Errorf("command = %v, want %q", got, "sam send")
	}
	if accepted[0].Platform.OS != runtime.GOOS || accepted[0].Platform.Arch != runtime.GOARCH {
		t.Errorf("platform = %+v, want this host's %s/%s", accepted[0].Platform, runtime.GOOS, runtime.GOARCH)
	}
	if accepted[0].Platform.AppVersion != samversion.Version {
		t.Errorf("platform.appVersion = %q, want this build's %q", accepted[0].Platform.AppVersion, samversion.Version)
	}
	if len(surfaces) != 1 || surfaces[0] != httputil.SurfaceCLI {
		t.Errorf("client surface header = %v, want %q", surfaces, httputil.SurfaceCLI)
	}
	if len(auths) != 1 || auths[0] != "Bearer platform-token" {
		t.Errorf("authorization = %v, want the bearer the command itself used", auths)
	}
	if events := local.all(); len(events) != 0 {
		t.Fatalf("local sink delivered %d events, want none: the run belongs to the addressed install", len(events))
	}
}

func TestSplitPlatformOriginOnV2ReceivesTheRunOnce(t *testing.T) {
	rec, gw := newOrigin(t, platformV2Route, platformV1Route)
	opts := testOptions(t, &captureSink{})
	grantConsent(t, opts)
	opts.Gateway = gw
	var logs bytes.Buffer
	opts.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	Report(t.Context(), trackedCmd(t), nil, opts)

	if strings.Contains(logs.String(), "path="+platformIngestPath) {
		t.Errorf("the v2 route's 204 was logged as a refusal:\n%s", logs.String())
	}

	attempts, accepted, _, _ := rec.snapshot()
	want := []string{"/api/v1/productAnalytics/ingest/cli", "/api/v2/platform/productAnalytics/ingest/cli"}
	if !slices.Equal(attempts, want) {
		t.Fatalf("attempts = %v, want the gateway route then the platform's v2 one %v", attempts, want)
	}
	if methods := rec.attemptedMethods(); methods[1] != http.MethodPut {
		t.Errorf("the v2 route was asked with %s, want PUT", methods[1])
	}
	if len(accepted) != 1 || len(accepted[0].Events) != 1 || accepted[0].Events[0].Event != productanalytics.EventCLICommandRun {
		t.Fatalf("accepted %+v, want one report of one cli:command_run", accepted)
	}
}

func TestColocatedDeploymentReportsTheRunOnlyOnce(t *testing.T) {
	rec, gw := newOrigin(t, gatewayRoute, platformV2Route, platformV1Route)
	opts := testOptions(t, &captureSink{})
	grantConsent(t, opts)
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	attempts, accepted, _, _ := rec.snapshot()
	if len(attempts) != 1 || attempts[0] != "/api/v1/productAnalytics/ingest/cli" {
		t.Fatalf("attempts = %v, want the gateway route alone", attempts)
	}
	if len(accepted) != 1 {
		t.Fatalf("deployment accepted %d reports, want exactly 1", len(accepted))
	}
}

func TestARefusalThatIsNotAMissingRouteIsNotRetriedElsewhere(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		rec := &ingestRecorder{status: status}
		srv := httptest.NewServer(rec)
		opts := testOptions(t, &captureSink{})
		grantConsent(t, opts)
		opts.Gateway = Gateway{BaseURL: srv.URL, Token: "t", AllowPlainHTTP: true}

		Report(t.Context(), trackedCmd(t), nil, opts)
		srv.Close()

		if hits := rec.hits(); hits != 1 {
			t.Errorf("status %d: deployment received %d reports, want 1", status, hits)
		}
	}
}

func TestAnUnreachableOriginIsNotAskedTwice(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var accepts atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})

	opts := testOptions(t, &captureSink{})
	grantConsent(t, opts)
	opts.Gateway = Gateway{BaseURL: "http://" + listener.Addr().String(), Token: "t", AllowPlainHTTP: true}

	Report(t.Context(), trackedCmd(t), nil, opts)

	if got := accepts.Load(); got != 1 {
		t.Fatalf("origin was dialled %d times, want 1: a transport failure ends the attempt", got)
	}
}

func TestPlainHTTPPlatformWithoutInsecureSendsNothingOnEitherRoute(t *testing.T) {
	rec, gw := newOrigin(t, platformV2Route, platformV1Route)
	gw.AllowPlainHTTP = false
	opts := testOptions(t, &captureSink{})
	opts.Gateway = gw

	Report(t.Context(), trackedCmd(t), nil, opts)

	if attempts, _, _, _ := rec.snapshot(); len(attempts) != 0 {
		t.Fatalf("origin received %v, want nothing: a bearer token must not go over plain http", attempts)
	}
}
