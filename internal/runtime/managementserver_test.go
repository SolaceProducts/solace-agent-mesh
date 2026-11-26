// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type healthTestComponent struct {
	healthErr error
}

func (c *healthTestComponent) Init(context.Context, config.Config) error { return nil }
func (c *healthTestComponent) Start(context.Context) error               { return nil }
func (c *healthTestComponent) Stop(context.Context) error                { return nil }
func (c *healthTestComponent) Health() error                             { return c.healthErr }

func TestHealthEndpointHealthy(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)
	rt.Register("ok", &healthTestComponent{})

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hs.Stop(ctx) }()

	resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body healthResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&body); decErr != nil {
		t.Fatal(decErr)
	}
	if body.Status != "healthy" {
		t.Errorf("status = %q, want healthy", body.Status)
	}
}

func TestHealthEndpointUnhealthy(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)
	rt.Register("bad", &healthTestComponent{healthErr: errors.New("disk full")})

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hs.Stop(ctx) }()

	resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}

	var body healthResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&body); decErr != nil {
		t.Fatal(decErr)
	}
	if body.Status != "unhealthy" {
		t.Errorf("status = %q, want unhealthy", body.Status)
	}
	if body.Error == "" {
		t.Error("expected error message")
	}
}

func TestReadyEndpointNotReady(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	defer func() { _ = hs.Stop(ctx) }()

	resp, err := http.Get(fmt.Sprintf("http://%s/ready", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}

	var body readyResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&body); decErr != nil {
		t.Fatal(decErr)
	}
	if body.Ready {
		t.Error("expected ready=false")
	}
}

func TestReadyEndpointReady(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)

	ctx := context.Background()
	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hs.Stop(ctx) }()

	resp, err := http.Get(fmt.Sprintf("http://%s/ready", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body readyResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&body); decErr != nil {
		t.Fatal(decErr)
	}
	if !body.Ready {
		t.Error("expected ready=true")
	}
	if got := body.Checks["broker"]; got != "connected" {
		t.Errorf("checks.broker = %q, want connected", got)
	}
	if got := body.Checks["phase"]; got != "running" {
		t.Errorf("checks.phase = %q, want running", got)
	}
}

func TestReadyEndpointBrokerDown(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)

	ctx := t.Context()
	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hs.Stop(ctx) }()

	resp, err := http.Get(fmt.Sprintf("http://%s/ready", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}

	var body readyResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&body); decErr != nil {
		t.Fatal(decErr)
	}
	if body.Ready {
		t.Error("expected ready=false when broker disconnected")
	}
	if got := body.Checks["broker"]; got != "down" {
		t.Errorf("checks.broker = %q, want down", got)
	}
}

func TestReadyEndpointBrokerAbsent(t *testing.T) {
	rt := NewRuntime(nil, nil)

	ctx := t.Context()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hs.Stop(ctx) }()

	resp, err := http.Get(fmt.Sprintf("http://%s/ready", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body readyResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&body); decErr != nil {
		t.Fatal(decErr)
	}
	if !body.Ready {
		t.Error("expected ready=true when no broker is registered")
	}
	if got := body.Checks["broker"]; got != "absent" {
		t.Errorf("checks.broker = %q, want absent", got)
	}
}

func TestHealthServerStopGraceful(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if stopErr := hs.Stop(ctx); stopErr != nil {
		t.Fatal(stopErr)
	}

	_, err = http.Get(fmt.Sprintf("http://%s/health", addr))
	if err == nil {
		t.Error("expected error after server stopped")
	}
}

func TestManagementServerStopBoundsUnboundedContext(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)

	ms := NewManagementServer(rt, ":0", nil)
	if _, err := ms.Start(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := ms.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= managementShutdownTimeout {
		t.Errorf("Stop took %v; expected prompt return well under the %v bound",
			elapsed, managementShutdownTimeout)
	}
}

func TestManagementServerStopRespectsCallerDeadline(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)

	ms := NewManagementServer(rt, ":0", nil)
	if _, err := ms.Start(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := ms.Stop(ctx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestHealthEndpointMultiComponent(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)
	rt.Register("healthy-a", &healthTestComponent{})
	rt.Register("failing-mid", &healthTestComponent{healthErr: errors.New("simulated failure")})
	rt.Register("healthy-b", &healthTestComponent{})
	rt.Register("also-failing-end", &healthTestComponent{healthErr: errors.New("second failure")})

	ctx := t.Context()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hs.Stop(ctx) }()

	resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}

	var body healthResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&body); decErr != nil {
		t.Fatal(decErr)
	}
	if !strings.Contains(body.Error, "failing-mid") {
		t.Errorf("error %q does not name the first failing component", body.Error)
	}
	if !strings.Contains(body.Error, "simulated failure") {
		t.Errorf("error %q does not include the underlying error", body.Error)
	}
	if strings.Contains(body.Error, "also-failing-end") {
		t.Errorf("error %q names the later failing component; Health() should short-circuit at the first failure", body.Error)
	}
}

func TestHealthEndpointRecovery(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)
	comp := &healthTestComponent{healthErr: errors.New("startup failure")}
	rt.Register("recoverable", comp)

	ctx := t.Context()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hs.Stop(ctx) }()

	resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("initial status = %d, want 503", resp.StatusCode)
	}

	comp.healthErr = nil

	resp, err = http.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("recovered status = %d, want 200", resp.StatusCode)
	}
}

func TestHealthEndpointJSONShape(t *testing.T) {
	type registered struct {
		name string
		comp *healthTestComponent
	}
	cases := []struct {
		name          string
		components    []registered
		nilBroker     bool
		connectBroker bool
		start         bool
		endpoint      string
		wantStatus    int
		wantBody      string
	}{
		{
			name:       "health-healthy",
			components: []registered{{"ok", &healthTestComponent{}}},
			start:      true,
			endpoint:   "/health",
			wantStatus: http.StatusOK,
			wantBody:   "{\"status\":\"healthy\"}\n",
		},
		{
			name:       "health-unhealthy",
			components: []registered{{"bad", &healthTestComponent{healthErr: errors.New("disk full")}}},
			start:      true,
			endpoint:   "/health",
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "{\"status\":\"unhealthy\",\"error\":\"component bad unhealthy: disk full\"}\n",
		},
		{
			name:          "ready-true-broker-connected",
			connectBroker: true,
			start:         true,
			endpoint:      "/ready",
			wantStatus:    http.StatusOK,
			wantBody:      "{\"ready\":true,\"checks\":{\"broker\":\"connected\",\"phase\":\"running\"}}\n",
		},
		{
			name:       "ready-false-broker-down",
			start:      true,
			endpoint:   "/ready",
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "{\"ready\":false,\"checks\":{\"broker\":\"down\",\"phase\":\"running\"}}\n",
		},
		{
			name:       "ready-true-broker-absent",
			nilBroker:  true,
			start:      true,
			endpoint:   "/ready",
			wantStatus: http.StatusOK,
			wantBody:   "{\"ready\":true,\"checks\":{\"broker\":\"absent\",\"phase\":\"running\"}}\n",
		},
		{
			name:       "ready-false-not-started",
			start:      false,
			endpoint:   "/ready",
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "{\"ready\":false,\"checks\":{\"broker\":\"down\",\"phase\":\"created\"}}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b broker.Broker
			if !tc.nilBroker {
				b = broker.NewDevBroker()
			}
			rt := NewRuntime(b, nil)
			for _, r := range tc.components {
				rt.Register(r.name, r.comp)
			}
			ctx := t.Context()
			if tc.connectBroker {
				if err := b.Connect(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if tc.start {
				if err := rt.Init(ctx); err != nil {
					t.Fatal(err)
				}
				if err := rt.Start(ctx); err != nil {
					t.Fatal(err)
				}
			}

			hs := NewManagementServer(rt, ":0", nil)
			addr, err := hs.Start()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = hs.Stop(ctx) }()

			resp, err := http.Get(fmt.Sprintf("http://%s%s", addr, tc.endpoint))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}

			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.wantBody {
				t.Errorf("body = %q, want %q", string(got), tc.wantBody)
			}
		})
	}
}

func TestHealthEndpointConcurrent(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)
	rt.Register("ok", &healthTestComponent{})

	ctx := t.Context()
	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	hs := NewManagementServer(rt, ":0", nil)
	addr, err := hs.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hs.Stop(ctx) }()

	const N = 50
	var wg sync.WaitGroup
	errCh := make(chan error, N*2)

	fire := func(path string) {
		resp, err := http.Get(fmt.Sprintf("http://%s%s", addr, path))
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			errCh <- fmt.Errorf("%s status = %d, want 200", path, resp.StatusCode)
		}
	}

	for range N {
		wg.Go(func() { fire("/health") })
		wg.Go(func() { fire("/ready") })
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent request: %v", err)
	}
}

func TestCmdHealthAddrDefaults(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path        string
		wantDefault string
	}{
		{"cmd/gateway/main.go", ":9090"},
		{"cmd/awe/main.go", ":8090"},
		{"cmd/str/main.go", ":8090"},
	}

	flagDefaultRE := regexp.MustCompile(`flag\.(?:String|StringVar)\([^)]*"health-addr"\s*,\s*"([^"]+)"`)
	guardRE := regexp.MustCompile(`if\s+\*?healthAddr\s*!=\s*""\s*{`)

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			full := filepath.Join(repoRoot, tc.path)
			data, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}

			m := flagDefaultRE.FindStringSubmatch(string(data))
			if m == nil {
				t.Fatalf("could not find health-addr flag declaration in %s", tc.path)
			}
			if m[1] != tc.wantDefault {
				t.Errorf("default = %q, want %q", m[1], tc.wantDefault)
			}

			if !guardRE.MatchString(string(data)) {
				t.Errorf("missing empty-string disable guard (`if healthAddr != \"\"` or `if *healthAddr != \"\"`) in %s", tc.path)
			}
		})
	}
}

func TestManagementServer_Handle(t *testing.T) {
	rt := NewRuntime(broker.NewDevBroker(), config.FromMap(nil))
	ms := NewManagementServer(rt, ":0", nil)

	ms.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP test\n"))
	}))

	addr, err := ms.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Stop(context.Background()) })

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/metrics: got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "# HELP") {
		t.Errorf("/metrics body: got %q, want exposition format", string(body))
	}

	resp2, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("/health: got %d after Handle registration, want 200", resp2.StatusCode)
	}
}
