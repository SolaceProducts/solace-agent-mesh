// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func advisoryRig(t *testing.T, maxMediaSizeMB int, enforceAuthz bool) (*Adapter, *bytes.Buffer) {
	t.Helper()
	core := newTestCore(t)
	if enforceAuthz {
		core.SetAuthzEnabled(true)
	}
	var buf bytes.Buffer
	return &Adapter{
		core:   core,
		cfg:    &WhatsAppConfig{MaxMediaSizeMB: maxMediaSizeMB},
		logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, &buf
}

func guardCfg(t *testing.T) map[string]any {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.OK"}]}`))
	}))
	t.Cleanup(srv.Close)
	return map[string]any{
		"phone_number_id":    "555111",
		"access_token":       "tok-test",
		"app_secret":         "secret",
		"verify_token":       "verify",
		"graph_api_base_url": srv.URL,
	}
}

func TestInit_RefusesDisableRateLimitingAgainstProductionGraph(t *testing.T) {
	t.Parallel()
	cfg := guardCfg(t)
	delete(cfg, "graph_api_base_url")
	cfg["disable_rate_limiting"] = true

	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	initErr := a.Init(t.Context(), newTestCore(t))
	if initErr == nil {
		t.Fatal("Init succeeded with disable_rate_limiting against the production Graph API")
	}
	if !strings.Contains(initErr.Error(), "disable_rate_limiting") {
		t.Errorf("error should name the offending key: %v", initErr)
	}
	if !strings.Contains(initErr.Error(), defaultGraphAPIBaseURL) {
		t.Errorf("error should name the Graph host that triggered the refusal: %v", initErr)
	}
}

func TestInit_AllowsDisableRateLimitingAgainstAMock(t *testing.T) {
	t.Parallel()
	cfg := guardCfg(t)
	cfg["disable_rate_limiting"] = true

	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if err := a.Init(t.Context(), newTestCore(t)); err != nil {
		t.Fatalf("Init must accept disable_rate_limiting against a non-production Graph host: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(t.Context()) })
}

func TestCheckTestOnlyConfig(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		cfg       *WhatsAppConfig
		wantError bool
	}{
		{"default host, limiting on", &WhatsAppConfig{GraphAPIBaseURL: defaultGraphAPIBaseURL}, false},
		{"default host, limiting off", &WhatsAppConfig{GraphAPIBaseURL: defaultGraphAPIBaseURL, DisableRateLimiting: true}, true},
		{"mock host, limiting off", &WhatsAppConfig{GraphAPIBaseURL: "http://127.0.0.1:9999", DisableRateLimiting: true}, false},
		{"mock host, limiting on", &WhatsAppConfig{GraphAPIBaseURL: "http://127.0.0.1:9999"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := checkTestOnlyConfig(tt.cfg)
			if (err != nil) != tt.wantError {
				t.Errorf("checkTestOnlyConfig error = %v, want error = %v", err, tt.wantError)
			}
		})
	}
}

func TestStartupAdvisories_OpenConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name         string
		enforceAuthz bool
		wantPhrase   string
	}{
		{
			name:       "no authz warns that anyone can start a task",
			wantPhrase: "accepts messages from any sender",
		},
		{
			name:         "enforced authorization still warns about the open door",
			enforceAuthz: true,
			wantPhrase:   "does not restrict senders",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := guardCfg(t)
			a, buf := advisoryRig(t, 25, tt.enforceAuthz)
			a.logStartupAdvisories(cfg)

			out := buf.String()
			for _, phrase := range []string{"accepts messages from any sender", "does not restrict senders"} {
				want := phrase == tt.wantPhrase
				if got := strings.Contains(out, phrase); got != want {
					t.Errorf("warning %q present = %v, want %v; output = %q", phrase, got, want, out)
				}
			}
		})
	}
}

func TestStartupAdvisories_AnnouncesDefaultedMediaCap(t *testing.T) {
	t.Run("absent key is announced with the value", func(t *testing.T) {
		a, buf := advisoryRig(t, defaultMaxMediaSizeMB, false)
		a.logStartupAdvisories(guardCfg(t))
		out := buf.String()
		if !strings.Contains(out, "media size cap using the default") {
			t.Fatalf("no defaulted-cap announcement in %q", out)
		}
		if want := fmt.Sprintf("maxMediaSizeMb=%d", defaultMaxMediaSizeMB); !strings.Contains(out, want) {
			t.Errorf("announcement must carry the effective cap %q; got %q", want, out)
		}
	})

	t.Run("an explicit key is not announced", func(t *testing.T) {
		cfg := guardCfg(t)
		cfg["max_media_size_mb"] = 10
		a, buf := advisoryRig(t, 10, false)
		a.logStartupAdvisories(cfg)
		if out := buf.String(); strings.Contains(out, "media size cap using the default") {
			t.Errorf("an explicitly configured cap must not be announced as a default: %q", out)
		}
	})
}

type initAdvisoryCapture struct {
	store     *initAdvisoryStore
	gatewayID string
	attrs     []slog.Attr
}

type initAdvisoryStore struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *initAdvisoryCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *initAdvisoryCapture) Handle(_ context.Context, r slog.Record) error {
	for _, a := range c.attrs {
		if a.Key == "gatewayID" && a.Value.String() == c.gatewayID {
			c.store.mu.Lock()
			c.store.records = append(c.store.records, r)
			c.store.mu.Unlock()
			return nil
		}
	}
	return nil
}

func (c *initAdvisoryCapture) WithAttrs(as []slog.Attr) slog.Handler {
	return &initAdvisoryCapture{store: c.store, gatewayID: c.gatewayID, attrs: append(slices.Clone(c.attrs), as...)}
}

func (c *initAdvisoryCapture) WithGroup(string) slog.Handler { return c }

func TestInit_EmitsStartupAdvisories(t *testing.T) {
	const gatewayID = "wa-advisory-wiring"
	store := &initAdvisoryStore{}
	prev := slog.Default()
	slog.SetDefault(slog.New(&initAdvisoryCapture{store: store, gatewayID: gatewayID}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, testAppConfig(t, gatewayID))
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	initCoreForTest(t, core)

	a, err := NewAdapter(guardCfg(t))
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(t.Context()) })

	store.mu.Lock()
	records := slices.Clone(store.records)
	store.mu.Unlock()

	for _, want := range []struct {
		fragment string
		level    slog.Level
	}{
		{"accepts messages from any sender", slog.LevelWarn},
		{"media size cap using the default", slog.LevelInfo},
	} {
		var found bool
		for _, r := range records {
			if !strings.Contains(r.Message, want.fragment) {
				continue
			}
			found = true
			if r.Level != want.level {
				t.Errorf("advisory %q logged at %s, want %s", want.fragment, r.Level, want.level)
			}
		}
		if !found {
			t.Errorf("Init emitted no advisory containing %q; got %d records", want.fragment, len(records))
		}
	}
}

func TestStartupAdvisories_RetiredAllowlistIsCalledOut(t *testing.T) {
	const warned = "no longer restricts anyone"

	for _, tt := range []struct {
		name  string
		runAs string
		keys  map[string]any
		want  bool
	}{
		{
			name:  "a scoped run_as has no other signal",
			runAs: "ops",
			keys:  map[string]any{"allowed_senders": []any{"15551230001"}},
			want:  true,
		},
		{
			name: "prefixes alone count too",
			keys: map[string]any{"allowed_prefixes": "1555"},
			want: true,
		},
		{
			name: "a config that never had one stays quiet",
			want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := guardCfg(t)
			for k, v := range tt.keys {
				cfg[k] = v
			}
			a, buf := advisoryRig(t, 25, true)
			a.cfg.RunAs = tt.runAs

			a.logStartupAdvisories(cfg)

			if got := strings.Contains(buf.String(), warned); got != tt.want {
				t.Errorf("warning present = %v, want %v; output = %q", got, tt.want, buf.String())
			}
			for key := range tt.keys {
				if tt.want && !strings.Contains(buf.String(), key) {
					t.Errorf("warning does not name %q, so an operator cannot find what to delete", key)
				}
			}
		})
	}
}
