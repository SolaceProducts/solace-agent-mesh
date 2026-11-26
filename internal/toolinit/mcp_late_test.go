// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	bifrostMCP "github.com/maximhq/bifrost/core/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestMain(m *testing.M) {
	bifrostMCP.ConnectRetryConfig = bifrostMCP.RetryConfig{MaxRetries: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	os.Exit(m.Run())
}

func useFastLateMCPRetries(t *testing.T) {
	t.Helper()
	savedDelays := lateMCPRetryDelays
	lateMCPRetryDelays = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond}
	t.Cleanup(func() { lateMCPRetryDelays = savedDelays })
}

func newFlakyRunnerServer(t *testing.T, status int) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	s := server.NewMCPServer("runner", "1.0.0")
	s.AddTool(mcpgo.NewTool("job_status", mcpgo.WithDescription("reports a job's phase"), mcpgo.WithReadOnlyHintAnnotation(true), mcpgo.WithDestructiveHintAnnotation(false), mcpgo.WithOpenWorldHintAnnotation(false)),
		func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			return mcpgo.NewToolResultText("Succeeded"), nil
		})
	mcpHandler := server.NewStreamableHTTPServer(s)
	var up atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, http.StatusText(status), status)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &up
}

func runnerEntry(url string, extra map[string]any) map[string]any {
	entry := mcpEntry(url, map[string]any{"source_connector_id": "conn-runner"}, nil)
	for k, v := range extra {
		entry[k] = v
	}
	return entry
}

func startSubComponents(t *testing.T, host *testHost) {
	t.Helper()
	host.mu.Lock()
	subs := slices.Clone(host.subs)
	host.mu.Unlock()
	for _, sc := range subs {
		if err := sc.Start(t.Context()); err != nil {
			t.Fatalf("start sub-component: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, sc := range subs {
			sc.Stop()
		}
	})
}

func lateRegistrarOf(t *testing.T, host *testHost) *lateMCPRegistrar {
	t.Helper()
	host.mu.Lock()
	defer host.mu.Unlock()
	for _, sc := range host.subs {
		if r, ok := sc.(*lateMCPRegistrar); ok {
			return r
		}
	}
	return nil
}

func dispatch(t *testing.T, host *testHost, name string) {
	t.Helper()
	res, err := host.Tools().Dispatch(t.Context(), tool.Call{ID: "call-" + name, Name: name, Arguments: "{}"}, &tool.Context{})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	if res == nil || res.Status == "error" {
		t.Fatalf("calling %s: result %+v", name, res)
	}
}

type lateRegistration struct {
	recordIndex int
	rec         EntryRecord
}

func TestRegisterTools_UnreachableMCPToolsetRegistersLate(t *testing.T) {
	useFastLateMCPRetries(t)
	liveURL := startSharedMCPServer(t, false)
	srv, up := newFlakyRunnerServer(t, http.StatusServiceUnavailable)
	host := newTestHost("late-agent")
	ready := make(chan lateRegistration, 1)

	toolsCfg := []any{
		mcpEntry(liveURL, map[string]any{"tool_name": "get_weather"}, nil),
		runnerEntry(srv.URL, nil),
	}
	_, entries, err := RegisterTools(host, toolsCfg, newTestServices(t), Options{
		OnLateMCPRegistration: func(recordIndex int, rec EntryRecord) { ready <- lateRegistration{recordIndex, rec} },
	})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if len(entries) != 2 || entries[1].Status != EntryStatusFailed || SourceConnectorID(entries[1].ToolMap) != "conn-runner" {
		t.Fatalf("entries = %+v, want the runner's entry second and failed", entries)
	}
	if host.Tools().GetTool("job_status") != nil {
		t.Fatal("job_status registered while the server was unreachable")
	}

	startSubComponents(t, host)
	up.Store(true)

	select {
	case got := <-ready:
		if got.recordIndex != 1 {
			t.Errorf("recordIndex = %d, want 1, the runner's failed record", got.recordIndex)
		}
		if got.rec.Status != EntryStatusReady || got.rec.Error != "" || !slices.Equal(got.rec.ToolNames, []string{"job_status"}) {
			t.Fatalf("late record = %+v, want ready with job_status", got.rec)
		}
		if SourceConnectorID(got.rec.ToolMap) != "conn-runner" {
			t.Errorf("late record connector = %q, want conn-runner", SourceConnectorID(got.rec.ToolMap))
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the toolset never registered after the server came up")
	}

	dispatch(t, host, "job_status")
	dispatch(t, host, "get_weather")
}

func TestRegisterTools_LateMCPToolsTakeTheirEntrysTrustInAnnotations(t *testing.T) {
	useFastLateMCPRetries(t)
	untrustedSrv, untrustedUp := newFlakyRunnerServer(t, http.StatusServiceUnavailable)
	trustedSrv, trustedUp := newFlakyRunnerServer(t, http.StatusServiceUnavailable)
	host := newTestHost("late-trust-agent")
	ready := make(chan lateRegistration, 2)

	toolsCfg := []any{
		runnerEntry(untrustedSrv.URL, map[string]any{"tool_name_prefix": "untrusted_"}),
		runnerEntry(trustedSrv.URL, map[string]any{"tool_name_prefix": "trusted_", "trust_annotations": true}),
	}
	if _, _, err := RegisterTools(host, toolsCfg, newTestServices(t), Options{
		OnLateMCPRegistration: func(recordIndex int, rec EntryRecord) { ready <- lateRegistration{recordIndex, rec} },
	}); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	startSubComponents(t, host)
	untrustedUp.Store(true)
	trustedUp.Store(true)
	for range 2 {
		select {
		case <-ready:
		case <-time.After(20 * time.Second):
			t.Fatal("the toolsets never registered after the servers came up")
		}
	}

	for name, want := range map[string]tool.EffectClass{
		"untrusted_job_status": tool.UntrustedHintEffect(tool.EffectSideEffecting),
		"trusted_job_status":   tool.HintedEffect(tool.EffectReadOnly),
	} {
		registered := host.Tools().GetTool(name)
		if registered == nil {
			t.Errorf("%s is not registered", name)
			continue
		}
		if got := tool.EffectClassOf(registered); got != want {
			t.Errorf("%s: EffectClassOf = %+v, want %+v", name, got, want)
		}
	}
}

func TestRegisterTools_UnreachableMCPEntriesSharingAServerBothRegisterLate(t *testing.T) {
	useFastLateMCPRetries(t)
	srv, up := newFlakyRunnerServer(t, http.StatusServiceUnavailable)
	host := newTestHost("shared-late-agent")
	ready := make(chan lateRegistration, 2)

	toolsCfg := []any{
		runnerEntry(srv.URL, map[string]any{"tool_name": "job_status"}),
		runnerEntry(srv.URL, map[string]any{"tool_name_prefix": "p_"}),
	}
	_, _, err := RegisterTools(host, toolsCfg, newTestServices(t), Options{
		OnLateMCPRegistration: func(recordIndex int, rec EntryRecord) { ready <- lateRegistration{recordIndex, rec} },
	})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	startSubComponents(t, host)
	up.Store(true)

	got := map[int][]string{}
	for range 2 {
		select {
		case r := <-ready:
			got[r.recordIndex] = r.rec.ToolNames
		case <-time.After(20 * time.Second):
			t.Fatalf("only %d of 2 entries registered after the server came up: %v", len(got), got)
		}
	}
	if !slices.Equal(got[0], []string{"job_status"}) || !slices.Equal(got[1], []string{"p_job_status"}) {
		t.Fatalf("late registrations = %v, want record 0 job_status and record 1 p_job_status", got)
	}
}

func TestLateMCPRetriesDoNotStallOtherToolsetsWhileTheServerRefuses(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusNotFound, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			useFastLateMCPRetries(t)
			liveURL := startSharedMCPServer(t, false)
			var slow atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if slow.Load() {
					time.Sleep(time.Second)
				}
				http.Error(w, http.StatusText(status), status)
			}))
			t.Cleanup(srv.Close)
			host := newTestHost("stall-agent")

			toolsCfg := []any{
				mcpEntry(liveURL, map[string]any{"tool_name": "get_weather"}, nil),
				runnerEntry(srv.URL, nil),
			}
			if _, _, err := RegisterTools(host, toolsCfg, newTestServices(t), Options{
				OnLateMCPRegistration: func(int, EntryRecord) { t.Error("registered against a server that never came up") },
			}); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}
			slow.Store(true)
			startSubComponents(t, host)

			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				start := time.Now()
				dispatch(t, host, "get_weather")
				if took := time.Since(start); took > 500*time.Millisecond {
					t.Fatalf("a call on the live toolset took %v while the other server answered %d", took, status)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func TestLateMCPRegistrarStopsWhenItsStartContextEnds(t *testing.T) {
	useFastLateMCPRetries(t)
	srv, up := newFlakyRunnerServer(t, http.StatusServiceUnavailable)
	host := newTestHost("failed-start-agent")
	stopSubComponents(t, host)

	if _, _, err := RegisterTools(host, []any{runnerEntry(srv.URL, nil)}, newTestServices(t), Options{
		OnLateMCPRegistration: func(int, EntryRecord) { t.Error("registered on a host whose start was abandoned") },
	}); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	registrar := lateRegistrarOf(t, host)
	ctx, cancel := context.WithCancel(t.Context())
	if err := registrar.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()

	finished := make(chan struct{})
	go func() {
		registrar.done.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the retries kept running after the start context ended")
	}
	up.Store(true)
	time.Sleep(100 * time.Millisecond)
}

func TestRegisterTools_UnreachableMCPToolsetWithoutHookIsNotRetried(t *testing.T) {
	useFastLateMCPRetries(t)
	srv, _ := newFlakyRunnerServer(t, http.StatusServiceUnavailable)
	host := newTestHost("no-retry-agent")
	stopSubComponents(t, host)

	if _, _, err := RegisterTools(host, []any{runnerEntry(srv.URL, nil)}, newTestServices(t), Options{}); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if lateRegistrarOf(t, host) != nil {
		t.Fatal("a late registrar was added although the caller set no OnLateMCPRegistration")
	}
}

func TestRegisterTools_MCPToolsetsThatCannotRegisterLateAreNotRetried(t *testing.T) {
	cases := []struct {
		name   string
		status int
		extra  map[string]any
	}{
		{name: "approval gate", status: http.StatusServiceUnavailable, extra: map[string]any{"hil": map[string]any{"enabled": true}}},
		{name: "volume bindings", status: http.StatusServiceUnavailable, extra: map[string]any{"volume_bindings": map[string]any{"data": "vol"}}},
		{name: "network bindings", status: http.StatusServiceUnavailable, extra: map[string]any{"network_bindings": map[string]any{"net": "policy"}}},
		{name: "effect override", status: http.StatusServiceUnavailable, extra: map[string]any{"effect": "side_effecting"}},
		{name: "server refuses on auth grounds", status: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useFastLateMCPRetries(t)
			srv, _ := newFlakyRunnerServer(t, tc.status)
			host := newTestHost("not-retried-agent")
			stopSubComponents(t, host)

			_, entries, err := RegisterTools(host, []any{runnerEntry(srv.URL, tc.extra)}, newTestServices(t), Options{
				OnLateMCPRegistration: func(int, EntryRecord) { t.Error("a toolset that must not register late did") },
			})
			if err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}
			if len(entries) != 1 || entries[0].Status != EntryStatusFailed {
				t.Fatalf("entries = %+v, want one failed entry", entries)
			}
			if r := lateRegistrarOf(t, host); r == nil || len(r.pending) != 0 {
				t.Fatalf("registrar = %+v, want one with nothing pending", r)
			}
		})
	}
}

func TestRegisterTools_SiblingsOfAnMCPEntryWithBootOnlyPolicyAreNotRetried(t *testing.T) {
	useFastLateMCPRetries(t)
	gated, _ := newFlakyRunnerServer(t, http.StatusServiceUnavailable)
	other, _ := newFlakyRunnerServer(t, http.StatusServiceUnavailable)
	host := newTestHost("gated-sibling-agent")
	stopSubComponents(t, host)

	toolsCfg := []any{
		runnerEntry(gated.URL, map[string]any{"tool_name": "job_status", "hil": map[string]any{"enabled": true}}),
		runnerEntry(gated.URL+"/", map[string]any{"tool_name_prefix": "p_"}),
		mcpEntry(other.URL, map[string]any{"source_connector_id": "conn-other"}, nil),
	}
	_, entries, err := RegisterTools(host, toolsCfg, newTestServices(t), Options{
		OnLateMCPRegistration: func(int, EntryRecord) {},
	})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %+v, want three", entries)
	}
	registrar := lateRegistrarOf(t, host)
	if registrar == nil || len(registrar.pending) != 1 || registrar.pending[0].recordIndex != 2 {
		t.Fatalf("registrar = %+v, want only the entry on the other server pending", registrar)
	}
}

func TestLateMCPRegistrarStopDoesNotWaitOutAConnectInFlight(t *testing.T) {
	useFastLateMCPRetries(t)
	savedWait := lateMCPStopWait
	lateMCPStopWait = 100 * time.Millisecond
	t.Cleanup(func() { lateMCPStopWait = savedWait })

	s := server.NewMCPServer("runner", "1.0.0")
	s.AddTool(mcpgo.NewTool("job_status"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("Succeeded"), nil
	})
	mcpHandler := server.NewStreamableHTTPServer(s)
	var up, connecting atomic.Bool
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case !up.Load():
			http.Error(w, "starting", http.StatusServiceUnavailable)
		case r.Header.Get(mcpgo.HeaderMethod) == string(mcpgo.MethodToolsList):
			connecting.Store(true)
			<-hang
			mcpHandler.ServeHTTP(w, r)
		default:
			mcpHandler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	host := newTestHost("stop-mid-connect-agent")
	stopSubComponents(t, host)
	if _, _, err := RegisterTools(host, []any{runnerEntry(srv.URL, nil)}, newTestServices(t), Options{
		OnLateMCPRegistration: func(int, EntryRecord) { t.Error("registered after Stop") },
	}); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	registrar := lateRegistrarOf(t, host)
	if err := registrar.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(hang)
		registrar.done.Wait()
	})
	up.Store(true)
	deadline := time.Now().Add(5 * time.Second)
	for !connecting.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the registrar never began connecting")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	registrar.Stop()
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("Stop took %v with a connect in flight, want about %v", took, lateMCPStopWait)
	}
}

func TestLateMCPRegistrarStopsPromptlyWhileTheServerStaysDown(t *testing.T) {
	useFastLateMCPRetries(t)
	srv, _ := newFlakyRunnerServer(t, http.StatusServiceUnavailable)
	host := newTestHost("down-agent")
	stopSubComponents(t, host)

	if _, _, err := RegisterTools(host, []any{runnerEntry(srv.URL, nil)}, newTestServices(t), Options{
		OnLateMCPRegistration: func(int, EntryRecord) { t.Error("registered against a server that never came up") },
	}); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	registrar := lateRegistrarOf(t, host)
	if registrar == nil || len(registrar.pending) != 1 {
		t.Fatalf("registrar = %+v, want one with the unreachable toolset pending", registrar)
	}
	if err := registrar.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		registrar.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return while the retry loop was running")
	}
	if len(registrar.pending) != 0 {
		t.Error("a stopped registrar still holds toolsets to retry")
	}
}
