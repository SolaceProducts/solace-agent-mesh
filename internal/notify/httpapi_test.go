// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newTestCore(t *testing.T, withDB bool, overrides map[string]any) *NotifyCore {
	t.Helper()
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(ctx) })
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))

	appConfig := map[string]any{"namespace": "test-ns"}
	if withDB {
		appConfig["database_url"] = "sqlite://" + filepath.Join(t.TempDir(), "notify.db")
	}
	for k, v := range overrides {
		appConfig[k] = v
	}
	core := NewNotifyCore(appConfig, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(ctx) })
	return core
}

func do(core *NotifyCore, method, path, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	core.handler.ServeHTTP(rr, r)
	return rr
}

func TestHTTP_HealthBypassesAuthAndOK(t *testing.T) {
	core := newTestCore(t, false, nil)
	if rr := do(core, http.MethodGet, "/api/v1/notify/health", ""); rr.Code != http.StatusOK {
		t.Errorf("health = %d, want 200", rr.Code)
	}
}

func TestHTTP_InboxDisabledReturns503(t *testing.T) {
	core := newTestCore(t, false, nil)
	if rr := do(core, http.MethodGet, "/api/v1/notify/inbox", ""); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("inbox (no db) = %d, want 503", rr.Code)
	}
	if rr := do(core, http.MethodGet, "/api/v1/notify/preferences", ""); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("prefs (no db) = %d, want 503", rr.Code)
	}
}

func TestHTTP_SubmitValidation(t *testing.T) {
	core := newTestCore(t, true, nil)
	if rr := do(core, http.MethodPost, "/api/v1/notify", `{"title":"x"}`); rr.Code != http.StatusBadRequest {
		t.Errorf("missing message = %d, want 400", rr.Code)
	}
	if rr := do(core, http.MethodPost, "/api/v1/notify", `{"message":"x","mode":"input"}`); rr.Code != http.StatusBadRequest {
		t.Errorf("mode input = %d, want 400", rr.Code)
	}
	if rr := do(core, http.MethodPost, "/api/v1/notify", `{"message":"x","bogus":1}`); rr.Code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", rr.Code)
	}
}

func TestHTTP_SubmitAllFailedReturns502(t *testing.T) {
	core := newTestCore(t, false, map[string]any{
		"notify": map[string]any{
			"directory": []any{
				map[string]any{
					"user_id": "sam_dev_user",
					"channels": []any{
						map[string]any{"type": "webhook", "config": map[string]any{"url": "http://169.254.169.254/hook"}},
					},
				},
			},
		},
	})
	rr := do(core, http.MethodPost, "/api/v1/notify", `{"message":"x","channels":["webhook"]}`)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("all-failed submit = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHTTP_MarkReadBadUUID(t *testing.T) {
	core := newTestCore(t, true, nil)
	if rr := do(core, http.MethodPost, "/api/v1/notify/inbox/not-a-uuid/read", ""); rr.Code != http.StatusBadRequest {
		t.Errorf("bad uuid = %d, want 400", rr.Code)
	}
}

func TestHTTP_PreferencesValidation(t *testing.T) {
	core := newTestCore(t, true, nil)
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences", `{}`); rr.Code != http.StatusBadRequest {
		t.Errorf("missing channelType = %d, want 400", rr.Code)
	}
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences", `{"channelType":"email"}`); rr.Code != http.StatusBadRequest {
		t.Errorf("missing enabled = %d, want 400", rr.Code)
	}
}

func TestSummarizeDeliveries(t *testing.T) {
	cases := []struct {
		recs []DeliveryRecord
		want string
	}{
		{nil, "no_channels_matched"},
		{[]DeliveryRecord{{Status: "sent"}}, "delivered"},
		{[]DeliveryRecord{{Status: "sent"}, {Status: "failed"}}, "partial"},
		{[]DeliveryRecord{{Status: "failed"}}, "failed"},
		{[]DeliveryRecord{{Status: "skipped"}}, "skipped"},
	}
	for _, c := range cases {
		if got := summarizeDeliveries(c.recs); got != c.want {
			t.Errorf("summarize(%+v) = %q, want %q", c.recs, got, c.want)
		}
	}
}

func TestHealth_NilBeforeStartAndConnected(t *testing.T) {
	core := newTestCore(t, false, nil)
	if err := core.Health(); err != nil {
		t.Errorf("pre-start Health = %v, want nil", err)
	}
	if err := core.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := core.Health(); err != nil {
		t.Errorf("started+connected Health = %v, want nil", err)
	}
}
