// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fakeCategoryPrefs struct {
	disabled map[string]bool
	err      error
}

func (f fakeCategoryPrefs) DisabledCategories(_ context.Context, _ string) (map[string]bool, error) {
	return f.disabled, f.err
}

func TestDispatcher_CategoryMutedGatesInbox(t *testing.T) {
	fi := &fakeInbox{}
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{}), fi, slog.Default())
	d.SetCategoryPreferences(fakeCategoryPrefs{disabled: map[string]bool{CategoryScheduledTask: true}})

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "c1", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Category: CategoryScheduledTask, Message: "x",
	})
	if len(fi.calls) != 0 {
		t.Fatalf("muted inbox-gating category must not write the inbox, got %+v", fi.calls)
	}
	if recs != nil {
		t.Fatalf("expected no delivery records for a fully-gated category, got %+v", recs)
	}
}

func TestDispatcher_CategoryMutedHILKeepsInbox(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fi := &fakeInbox{}
	dir := newConfigDirectory(map[string][]Channel{
		"trent": {{Type: "webhook", Config: map[string]any{"url": srv.URL}}},
	})
	d := NewDispatcher(dir, fi, slog.Default())
	d.httpClient = srv.Client()
	d.skipURLValidation = true
	d.SetCategoryPreferences(fakeCategoryPrefs{disabled: map[string]bool{CategoryHIL: true}})

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "c2", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Category: CategoryHIL, Message: "x",
	})
	if len(fi.calls) != 1 {
		t.Fatalf("hil mute must keep the inbox, got %+v", fi.calls)
	}
	if hits != 0 {
		t.Fatalf("hil mute must suppress the out-of-band webhook, hits=%d", hits)
	}
	if len(recs) != 1 || recs[0].ChannelType != "web" || recs[0].Status != "sent" {
		t.Fatalf("expected only a web record, got %+v", recs)
	}
}

func TestDispatcher_UncategorizedNotGated(t *testing.T) {
	fi := &fakeInbox{}
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{}), fi, slog.Default())
	d.SetCategoryPreferences(fakeCategoryPrefs{disabled: map[string]bool{CategoryScheduledTask: true}})

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "c3", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Message: "x",
	})
	if len(fi.calls) != 1 || len(recs) != 1 {
		t.Fatalf("uncategorized request must deliver normally, calls=%d recs=%+v", len(fi.calls), recs)
	}
}

func TestDispatcher_CategoryLookupError_FailsOpen(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fi := &fakeInbox{}
	dir := newConfigDirectory(map[string][]Channel{
		"trent": {{Type: "webhook", Config: map[string]any{"url": srv.URL}}},
	})
	d := NewDispatcher(dir, fi, slog.Default())
	d.httpClient = srv.Client()
	d.skipURLValidation = true
	d.SetCategoryPreferences(fakeCategoryPrefs{
		disabled: map[string]bool{CategoryScheduledTask: true},
		err:      errors.New("db unavailable"),
	})

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "c4", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Category: CategoryScheduledTask, Message: "x",
	})
	if len(fi.calls) != 1 {
		t.Fatalf("fail-open must still write the inbox, got %+v", fi.calls)
	}
	if hits != 1 {
		t.Fatalf("fail-open must still deliver out-of-band, hits=%d", hits)
	}
	if len(recs) != 2 {
		t.Fatalf("fail-open should deliver web + webhook, got %+v", recs)
	}
}

func newCategoryTestCore(t *testing.T) (*NotifyCore, context.Context) {
	t.Helper()
	return newTestCore(t, true, nil), t.Context()
}

func TestNotifyCore_CategoryPreferencesHTTP(t *testing.T) {
	core, _ := newCategoryTestCore(t)

	getList := func() []CategoryPreferenceDTO {
		t.Helper()
		rr := httptest.NewRecorder()
		core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/preferences/categories?userId=trent", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET categories status = %d, body=%s", rr.Code, rr.Body.String())
		}
		var page PaginatedResponse[CategoryPreferenceDTO]
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode categories: %v", err)
		}
		return page.Data
	}
	byKey := func(cats []CategoryPreferenceDTO) map[string]CategoryPreferenceDTO {
		m := map[string]CategoryPreferenceDTO{}
		for _, c := range cats {
			m[c.Category] = c
		}
		return m
	}

	before := getList()
	if len(before) != len(Categories()) {
		t.Fatalf("want all %d catalog categories, got %+v", len(Categories()), before)
	}
	bm := byKey(before)
	for _, cat := range Categories() {
		c, ok := bm[cat.Key]
		if !ok || !c.Enabled || c.Label == "" {
			t.Fatalf("category %q should be present, labeled, and enabled by default, got %+v", cat.Key, c)
		}
	}

	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"category":"scheduled_task","enabled":false}`)
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences/categories?userId=trent", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT category status = %d, body=%s", rr.Code, rr.Body.String())
	}

	after := byKey(getList())
	if after[CategoryScheduledTask].Enabled {
		t.Fatalf("scheduled_task should be muted after opt-out, got %+v", after[CategoryScheduledTask])
	}
	if !after[CategoryAgentMessage].Enabled || !after[CategoryHIL].Enabled {
		t.Fatalf("other categories should remain enabled, got %+v", after)
	}

	rr = httptest.NewRecorder()
	bad := strings.NewReader(`{"category":"not_a_real_category","enabled":false}`)
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences/categories?userId=trent", bad))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown category = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	noEnabled := strings.NewReader(`{"category":"hil"}`)
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences/categories?userId=trent", noEnabled))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT without enabled = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	malformed := strings.NewReader(`{not json`)
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences/categories?userId=trent", malformed))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT malformed body = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestCategoryPreferences_CrossUserScopes(t *testing.T) {
	core, _ := newCategoryTestCore(t)

	alice := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"tool:other:invoke"}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notify/preferences/categories?userId=bob", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), alice))
	rr := httptest.NewRecorder()
	core.handleCategoryPreferencesList(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-user category read without scope = %d, want 403", rr.Code)
	}

	alice = auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:read_others"}})
	req = httptest.NewRequest(http.MethodGet, "/api/v1/notify/preferences/categories?userId=bob", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), alice))
	rr = httptest.NewRecorder()
	core.handleCategoryPreferencesList(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("cross-user category read with read-others = %d, want 200", rr.Code)
	}

	alice = auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:read_others"}})
	req = httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences/categories?userId=bob",
		strings.NewReader(`{"category":"hil","enabled":false}`))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), alice))
	rr = httptest.NewRecorder()
	core.handleCategoryPreferencesSet(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-user category write with only read-others = %d, want 403", rr.Code)
	}

	alice = auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:send_others"}})
	req = httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences/categories?userId=bob",
		strings.NewReader(`{"category":"hil","enabled":false}`))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), alice))
	rr = httptest.NewRecorder()
	core.handleCategoryPreferencesSet(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("cross-user category write with send-others = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestCategoryPreferences_ServiceUnavailableWithoutDB(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(ctx) })
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := NewNotifyCore(map[string]any{"namespace": "test-ns"}, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(ctx) })

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/preferences/categories?userId=trent", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET categories without DB = %d, want 503 (body=%s)", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences/categories?userId=trent",
		strings.NewReader(`{"category":"hil","enabled":false}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("PUT categories without DB = %d, want 503 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestCategoryCatalog_ShippedEntries(t *testing.T) {
	want := map[string]bool{
		CategoryScheduledTask:         true,
		CategoryAgentMessage:          true,
		CategoryHIL:                   false,
		CategoryChatShare:             true,
		CategoryBudgetSelfWarn:        true,
		CategoryBudgetSelfLimit:       true,
		CategoryBudgetMeshWarn:        true,
		CategoryBudgetMeshLimit:       true,
		CategoryBudgetSystemUserWarn:  true,
		CategoryBudgetSystemUserLimit: true,
	}

	got := Categories()
	if len(got) != len(want) {
		t.Fatalf("catalog has %d categories, want %d: %+v", len(got), len(want), got)
	}
	for _, c := range got {
		gatesInbox, known := want[c.Key]
		if !known {
			t.Errorf("unexpected category %q — add it to this test's expectations", c.Key)
			continue
		}
		if c.GatesInbox != gatesInbox {
			t.Errorf("category %q GatesInbox = %v, want %v", c.Key, c.GatesInbox, gatesInbox)
		}
		if c.Label == "" {
			t.Errorf("category %q has no label; it would render blank in preferences", c.Key)
		}
		delete(want, c.Key)
	}
	for missing := range want {
		t.Errorf("category %q is missing from the catalog, so its opt-out is unavailable", missing)
	}
}

func TestCategoryCatalog_BudgetAdminAlertsAreOfferedOnlyToBudgetAdmins(t *testing.T) {
	for key, wantScoped := range map[string]bool{
		CategoryBudgetSelfWarn: false, CategoryBudgetSelfLimit: false,
		CategoryBudgetMeshWarn: true, CategoryBudgetMeshLimit: true,
		CategoryBudgetSystemUserWarn: true, CategoryBudgetSystemUserLimit: true,
	} {
		cat, err := LookupCategory(key)
		if err != nil {
			t.Fatalf("LookupCategory(%q): %v", key, err)
		}
		for _, tc := range []struct {
			name   string
			scopes []string
			want   bool
		}{
			{"no budget scope", []string{"budget:*:read"}, !wantScoped},
			{"budget update", []string{"budget:*:update"}, true},
			{"budget assign", []string{"budget:*:assign"}, true},
		} {
			if got := cat.offeredTo(auth.NewTestSubject(auth.SubjectFields{Scopes: tc.scopes})); got != tc.want {
				t.Errorf("%s offered to a caller with %s = %v, want %v", key, tc.name, got, tc.want)
			}
		}
	}
}

func TestCategoryLookup_ChatShare(t *testing.T) {
	cat, err := LookupCategory(CategoryChatShare)
	if err != nil {
		t.Fatalf("LookupCategory(%q): %v", CategoryChatShare, err)
	}
	if !cat.GatesInbox {
		t.Error("muting chat shares should suppress the inbox row, not just out-of-band delivery")
	}
	if cat.Label == "" {
		t.Error("chat-share category needs a user-facing label")
	}
}

func TestCategoryCatalog_EveryCategoryHasAGroupAndGroupsAreAdjacent(t *testing.T) {
	seen := map[string]bool{}
	last := ""
	for _, c := range Categories() {
		if c.Group == "" {
			t.Errorf("category %q has no group", c.Key)
		}
		if c.Group != last && seen[c.Group] {
			t.Errorf("group %q is split: %q comes after another group", c.Group, c.Key)
		}
		seen[c.Group] = true
		last = c.Group
	}
}

func TestHTTP_BudgetCategoriesAreOffered(t *testing.T) {
	core := newTestCore(t, true, nil)
	body := `{"category":"` + CategoryBudgetSelfWarn + `","enabled":false}`
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences/categories", body); rr.Code != http.StatusOK {
		t.Fatalf("PUT category = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if code, resp := putCells(core, cell(CategoryBudgetMeshLimit, inboxChannelType, false)); code != http.StatusOK {
		t.Fatalf("PUT matrix cell = %d, want 200 (body=%s)", code, resp)
	}
}
