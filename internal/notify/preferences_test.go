// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fakePrefs struct{ disabled map[string]bool }

func (f fakePrefs) DisabledChannels(_ context.Context, _ string) (map[string]bool, error) {
	return f.disabled, nil
}

type failingPrefs struct{}

func (failingPrefs) DisabledChannels(_ context.Context, _ string) (map[string]bool, error) {
	return nil, errors.New("db unavailable")
}

func TestDispatcher_PreferencesFailClosed(t *testing.T) {
	dir := newConfigDirectory(map[string][]Channel{
		"trent": {{Type: "webhook", Config: map[string]any{"url": "https://example.com/hook"}}},
	})
	d := NewDispatcher(dir, nil, slog.Default())
	d.SetPreferences(failingPrefs{})

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "pf1", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Message: "x",
	})
	if len(recs) != 1 || recs[0].ChannelType != "webhook" || recs[0].Status != "skipped" {
		t.Fatalf("expected webhook skipped on prefs failure, got %+v", recs)
	}
}

func TestDispatcher_PreferenceGating(t *testing.T) {
	wh := Channel{Type: "webhook", Config: map[string]any{"url": "https://example.com/hook"}}
	dir := newConfigDirectory(map[string][]Channel{"trent": {wh}})
	d := NewDispatcher(dir, nil, slog.Default())
	d.SetPreferences(fakePrefs{disabled: map[string]bool{store.PrefKey("webhook", destinationKey(wh)): true}})

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "p1", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Message: "x",
	})
	if len(recs) != 1 || recs[0].ChannelType != "webhook" || recs[0].Status != "skipped" {
		t.Fatalf("expected webhook skipped by preference, got %+v", recs)
	}
}

func TestDispatcher_PreferenceDoesNotGateInbox(t *testing.T) {
	fi := &fakeInbox{}
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{}), fi, slog.Default())
	d.SetPreferences(fakePrefs{disabled: map[string]bool{"web": true}})

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "p2", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Message: "x",
	})
	if len(fi.calls) != 1 {
		t.Fatalf("inbox should still be written despite web opt-out, calls=%d", len(fi.calls))
	}
	if len(recs) != 1 || recs[0].ChannelType != "web" || recs[0].Status != "sent" {
		t.Fatalf("expected one sent web record, got %+v", recs)
	}
}

func TestNotifyCore_PreferencesHTTP(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	appConfig := map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
		"notify": map[string]any{
			"directory": []any{
				map[string]any{
					"user_id": "trent",
					"channels": []any{
						map[string]any{"type": "webhook", "config": map[string]any{"url": "https://hooks.example.com/abc?token=secret"}},
						map[string]any{"type": "connector", "config": map[string]any{"tool_name": "slack_send_message_x", "str_binary": "slack"}},
					},
				},
			},
		},
	}
	core := NewNotifyCore(appConfig, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	getList := func() []PreferenceDTO {
		t.Helper()
		rr := httptest.NewRecorder()
		core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/preferences?userId=trent", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET preferences status = %d", rr.Code)
		}
		var page PaginatedResponse[PreferenceDTO]
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode preferences: %v", err)
		}
		return page.Data
	}
	byType := func(prefs []PreferenceDTO) map[string]PreferenceDTO {
		m := map[string]PreferenceDTO{}
		for _, p := range prefs {
			m[p.ChannelType] = p
		}
		return m
	}

	before := byType(getList())
	if len(before) != 2 {
		t.Fatalf("want 2 reachable channels (webhook, connector), got %+v", before)
	}
	if wh := before["webhook"]; !wh.Enabled || wh.Destination != "hooks.example.com" {
		t.Fatalf("webhook should be enabled with host destination (never the token-bearing URL), got %+v", wh)
	}
	conn := before["connector"]
	connKey := conn.DestinationKey
	if connKey != "slack_send_message_x" || conn.Destination != "Slack" || !conn.Enabled {
		t.Fatalf("connector should be enabled, keyed by its send-tool, labeled by platform, got %+v", conn)
	}

	rr := httptest.NewRecorder()
	body := strings.NewReader(fmt.Sprintf(`{"channelType":"connector","destinationKey":%q,"enabled":false}`, connKey))
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences?userId=trent", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT preference status = %d, body=%s", rr.Code, rr.Body.String())
	}

	after := byType(getList())
	if after["connector"].Enabled {
		t.Fatalf("connector should be disabled after opt-out, got %+v", after["connector"])
	}
	if !after["webhook"].Enabled {
		t.Fatalf("webhook should remain enabled, got %+v", after["webhook"])
	}

	dis, err := core.prefs.DisabledChannels(ctx, "trent")
	if err != nil || !dis[store.PrefKey("connector", connKey)] {
		t.Fatalf("expected the connector destination disabled, got %v (err=%v)", dis, err)
	}
}

func TestHTTP_Preferences_OnlyConfirmedProactiveChannelsGetAToggle(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)
	confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)

	rr := do(core, http.MethodGet, "/api/v1/notify/preferences", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET preferences = %d; body=%s", rr.Code, rr.Body.String())
	}
	var page PaginatedResponse[PreferenceDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ChannelType != "teams" || !page.Data[0].Enabled {
		t.Fatalf("preferences = %+v, want only the confirmed teams channel, enabled", page.Data)
	}
}

func TestHTTP_Preferences_ConfirmedChannelKeepsItsToggleDuringReverify(t *testing.T) {
	core := newTestCore(t, true, nil)
	confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)
	const newCode = "WXYZ-2345"
	hash := hashVerificationCode(devUser, "teams", newCode)
	if err := core.channels.StartConfirmation(t.Context(), devUser, "teams", `{"conversation_id":"T2"}`, hash, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("start re-verify: %v", err)
	}

	teamsListed := func(state string) {
		t.Helper()
		rr := do(core, http.MethodGet, "/api/v1/notify/preferences", "")
		var page PaginatedResponse[PreferenceDTO]
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil || rr.Code != http.StatusOK {
			t.Fatalf("%s: GET preferences = %d (%v)", state, rr.Code, err)
		}
		if len(page.Data) != 1 || page.Data[0].ChannelType != "teams" {
			t.Fatalf("%s: preferences = %+v, want the confirmed teams channel", state, page.Data)
		}
	}
	teamsListed("re-verify pending")

	wrong := hashVerificationCode(devUser, "teams", "ZZZZ-ZZZZ")
	for range maxVerificationAttempts + 1 {
		if _, err := core.channels.ConfirmByCode(t.Context(), devUser, "teams", wrong, maxVerificationAttempts); err != nil {
			t.Fatalf("wrong guess: %v", err)
		}
	}
	if ch := findChannel(listChannels(t, core), "teams"); ch == nil || !ch.Confirmed || !ch.Locked {
		t.Fatalf("channel = %+v, want confirmed with a locked re-verify", ch)
	}
	teamsListed("re-verify locked")
}
