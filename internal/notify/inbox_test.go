// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fakeInbox struct {
	calls []struct{ userID, title, body, source, category string }
}

func (f *fakeInbox) Add(_ context.Context, userID, title, body string, _ map[string]any, source, category string) (string, error) {
	f.calls = append(f.calls, struct{ userID, title, body, source, category string }{userID, title, body, source, category})
	return "fake-id", nil
}

func TestDispatcher_WebInboxByDefault(t *testing.T) {
	fi := &fakeInbox{}
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{}), fi, slog.Default())

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "w1", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Title: "T", Message: "M", SenderAgent: "myagent",
	})

	if len(recs) != 1 || recs[0].ChannelType != "web" || recs[0].Status != "sent" {
		t.Fatalf("expected one sent web record, got %+v", recs)
	}
	if len(fi.calls) != 1 || fi.calls[0].userID != "trent" || fi.calls[0].body != "M" || fi.calls[0].source != "myagent" {
		t.Fatalf("unexpected inbox calls: %+v", fi.calls)
	}
}

func TestDispatcher_WebInboxSkippedWhenChannelsExcludeWeb(t *testing.T) {
	fi := &fakeInbox{}
	dir := newConfigDirectory(map[string][]Channel{
		"trent": {{Type: "email", Config: map[string]any{}}},
	})
	d := NewDispatcher(dir, fi, slog.Default())

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "w2", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Channels: []string{"email"}, Message: "M",
	})

	if len(fi.calls) != 0 {
		t.Errorf("inbox should not be written when channels exclude web, got %+v", fi.calls)
	}
	if len(recs) != 1 || recs[0].ChannelType != "email" || recs[0].Status != "skipped" {
		t.Fatalf("expected one skipped email record, got %+v", recs)
	}
}

func TestNotifyCore_InboxHTTP(t *testing.T) {
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
	}
	core := NewNotifyCore(appConfig, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	if _, err := core.inbox.Add(ctx, "trent", "Report ready", "done", map[string]any{"task_id": "t1"}, "notify_user", ""); err != nil {
		t.Fatalf("seed inbox: %v", err)
	}

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox?userId=trent", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var page PaginatedResponse[NotificationDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].Status != "unread" || page.Data[0].Title != "Report ready" {
		t.Fatalf("unexpected list: %+v", page)
	}
	if page.Meta.Pagination.Count != 1 {
		t.Errorf("pagination count = %d, want 1", page.Meta.Pagination.Count)
	}
	id := page.Data[0].ID

	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify/inbox/"+id+"/read?userId=trent", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("mark-read status = %d, body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox?userId=trent&status=unread", nil))
	var unread PaginatedResponse[NotificationDTO]
	_ = json.Unmarshal(rr.Body.Bytes(), &unread)
	if len(unread.Data) != 0 {
		t.Errorf("expected 0 unread after mark-read, got %d", len(unread.Data))
	}

	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("no userId status = %d, want 200 (identity's own inbox)", rr.Code)
	}
}

func TestNotifyCore_InboxMarkAllReadHTTP(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	for range 3 {
		if _, err := core.inbox.Add(ctx, "trent", "n", "b", nil, "notify_user", ""); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify/inbox/read-all?userId=trent", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("read-all status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if c, _ := core.inbox.CountByUser(ctx, "trent", store.InboxFilter{UnreadOnly: true}); c != 0 {
		t.Fatalf("expected 0 unread after read-all, got %d", c)
	}
}

func TestNotifyCore_InboxMarkUnreadHTTP(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	id, err := core.inbox.Add(ctx, "trent", "n", "b", nil, "notify_user", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := core.inbox.MarkRead(ctx, "trent", id); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify/inbox/"+id+"/unread?userId=trent", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("mark-unread status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if c, _ := core.inbox.CountByUser(ctx, "trent", store.InboxFilter{UnreadOnly: true}); c != 1 {
		t.Fatalf("expected 1 unread after mark-unread, got %d", c)
	}

	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify/inbox/00000000-0000-0000-0000-000000000000/unread?userId=trent", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("mark-unread of missing id = %d, want 404", rr.Code)
	}
}

func TestNotifyCore_InboxDeleteHTTP(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	id, err := core.inbox.Add(ctx, "trent", "n", "b", nil, "notify_user", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/notify/inbox/"+id+"?userId=trent", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if c, _ := core.inbox.CountByUser(ctx, "trent", store.InboxFilter{}); c != 0 {
		t.Fatalf("expected 0 after delete, got %d", c)
	}

	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/notify/inbox/"+id+"?userId=trent", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("delete of missing id = %d, want 404", rr.Code)
	}

	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/notify/inbox/not-a-uuid?userId=trent", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("delete with malformed id = %d, want 400", rr.Code)
	}
}

func TestNotifyCore_InboxClearAllHTTP(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	for range 3 {
		if _, err := core.inbox.Add(ctx, "trent", "n", "b", nil, "notify_user", ""); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/notify/inbox?userId=trent", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("clear-all status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if c, _ := core.inbox.CountByUser(ctx, "trent", store.InboxFilter{}); c != 0 {
		t.Fatalf("expected 0 after clear-all, got %d", c)
	}
	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/notify/inbox?userId=trent", nil))
	if rr.Code != http.StatusNoContent {
		t.Errorf("clear-all on empty inbox = %d, want 204", rr.Code)
	}
}

func TestNotifyCore_SubmitHTTP(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"title":"Done","message":"your task finished"}`)
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox", nil))
	var page PaginatedResponse[NotificationDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].Title != "Done" {
		t.Fatalf("expected the submitted notification in the inbox, got %+v", page.Data)
	}

	rr = httptest.NewRecorder()
	bad := strings.NewReader(`{"recipient_user_id":"x","message":"hi"}`)
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify", bad))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("snake_case body status = %d, want 400 (strict decode)", rr.Code)
	}
}

func TestInboxDestructive_CrossUserRequiresSendScope(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	alice := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:read_others"}})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/notify/inbox?userId=bob", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), alice))
	rr := httptest.NewRecorder()
	core.handleInboxClearAll(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-user clear-all with only read-others = %d, want 403", rr.Code)
	}

	alice = auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:send_others"}})
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/notify/inbox?userId=bob", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), alice))
	rr = httptest.NewRecorder()
	core.handleInboxClearAll(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("cross-user clear-all with send-others = %d, want 204", rr.Code)
	}
}

func TestInboxList_CrossUserRequiresScope(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	alice := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"tool:other:invoke"}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox?userId=bob", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), alice))
	rr := httptest.NewRecorder()
	core.handleInboxList(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-user read without scope = %d, want 403", rr.Code)
	}

	alice = auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:read_others"}})
	req = httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox?userId=bob", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), alice))
	rr = httptest.NewRecorder()
	core.handleInboxList(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("cross-user read with scope = %d, want 200", rr.Code)
	}
}

func TestNotifyCore_InboxCategory(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	const namespace = "test-ns"
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := NewNotifyCore(map[string]any{
		"namespace":    namespace,
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	categoryByTitle := map[string]string{
		"scheduled":     CategoryScheduledTask,
		"uncategorized": "",
		"unknown":       "not_a_catalog_category",
	}
	for title, category := range categoryByTitle {
		data, err := json.Marshal(a2a.NotifyRequest{
			RequestID: "cat-" + title, RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
			Category: category, Title: title, Message: "m",
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := brk.PublishGuaranteed(ctx, a2a.NotifyRequestTopic(namespace, "trent"), &broker.Message{Payload: data}); err != nil {
			t.Fatalf("publish NotifyRequest: %v", err)
		}
	}

	rows := listInboxUntil(t, core, len(categoryByTitle))
	for _, row := range rows {
		var title string
		if err := json.Unmarshal(row["title"], &title); err != nil {
			t.Fatalf("decode title: %v", err)
		}
		raw, present := row["category"]
		if title != "scheduled" {
			if present {
				t.Errorf("%s notification: category = %s, want the field omitted", title, raw)
			}
			continue
		}
		var got string
		if err := json.Unmarshal(raw, &got); err != nil || got != CategoryScheduledTask {
			t.Errorf("scheduled notification: category = %s (err=%v), want %q", raw, err, CategoryScheduledTask)
		}
	}

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox?userId=trent&category="+CategoryScheduledTask, nil))
	var filtered struct {
		Data []NotificationDTO `json:"data"`
		Meta paginationMeta    `json:"meta"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &filtered); rr.Code != http.StatusOK || err != nil {
		t.Fatalf("filtered list = %d %s (%v)", rr.Code, rr.Body.String(), err)
	}
	if len(filtered.Data) != 1 || filtered.Data[0].Title != "scheduled" || filtered.Meta.Pagination.Count != 1 {
		t.Errorf("filtered by %s = %+v (count %d), want only the scheduled notification", CategoryScheduledTask, filtered.Data, filtered.Meta.Pagination.Count)
	}

	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox?userId=trent&category=not_a_catalog_category", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("filter by an unknown category = %d, want 400", rr.Code)
	}

	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify/inbox/read-all?userId=trent&category=not_a_catalog_category", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("read-all of an unknown category = %d, want 400", rr.Code)
	}
	rr = httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify/inbox/read-all?userId=trent&category="+CategoryScheduledTask, nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("read-all of %s = %d, body=%s", CategoryScheduledTask, rr.Code, rr.Body.String())
	}
	if c, _ := core.inbox.CountByUser(ctx, "trent", store.InboxFilter{UnreadOnly: true, Category: CategoryScheduledTask}); c != 0 {
		t.Errorf("unread %s after its read-all = %d, want 0", CategoryScheduledTask, c)
	}
	if c, _ := core.inbox.CountByUser(ctx, "trent", store.InboxFilter{UnreadOnly: true}); c != len(categoryByTitle)-1 {
		t.Errorf("unread after a %s read-all = %d, want the other %d untouched", CategoryScheduledTask, c, len(categoryByTitle)-1)
	}
}

func listInboxUntil(t *testing.T, core *NotifyCore, want int) []map[string]json.RawMessage {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		rr := httptest.NewRecorder()
		core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox?userId=trent", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("list status = %d, body=%s", rr.Code, rr.Body.String())
		}
		var page struct {
			Data []map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(page.Data) >= want {
			return page.Data
		}
		if time.Now().After(deadline) {
			t.Fatalf("inbox holds %d notifications after 3s, want %d", len(page.Data), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
