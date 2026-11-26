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
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	benLogin = "ben@example.com"
	benUUID  = "0192F3A4-0000-7000-8000-000000000001"
	benKey   = "0192f3a4-0000-7000-8000-000000000001"
)

var ben = auth.NewTestSubject(auth.SubjectFields{UserID: benLogin, PlatformUserID: benUUID})

func newIdentityTestCore(t *testing.T, caller *auth.Subject) *NotifyCore {
	t.Helper()
	return newIdentityTestCoreWith(t, caller, nil)
}

func newIdentityTestCoreWith(t *testing.T, caller *auth.Subject, extra map[string]any) *NotifyCore {
	t.Helper()
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(ctx) })
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	appConfig := map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}
	maps.Copy(appConfig, extra)
	core := NewNotifyCore(appConfig, NotifyExtensions{AuthMiddleware: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), caller)))
		})
	}})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(ctx) })
	return core
}

func listInbox(t *testing.T, core *NotifyCore) []NotificationDTO {
	t.Helper()
	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET inbox = %d, body=%s", rr.Code, rr.Body.String())
	}
	var page PaginatedResponse[NotificationDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	return page.Data
}

func newWebhookDispatcher(t *testing.T, login string) (*Dispatcher, *store.DB, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	db, err := store.Open("sqlite://"+filepath.Join(t.TempDir(), "notify.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	inbox := store.NewNotificationRepo(db)
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{
		login: {{Type: "webhook", Config: map[string]any{"url": srv.URL}}},
	}), inbox, slog.Default())
	d.SetDeduplicator(inbox)
	d.SetCategoryPreferences(store.NewCategoryPreferenceRepo(db))
	d.httpClient = srv.Client()
	d.skipURLValidation = true
	return d, db, &hits
}

func TestDispatcher_PlatformRecipientKeysInboxAndReachesLoginDirectory(t *testing.T) {
	ctx := t.Context()
	d, db, hits := newWebhookDispatcher(t, benLogin)

	recs := d.Deliver(ctx, a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Title: "Budget", Message: "80%",
	})

	if summarizeDeliveries(recs) != "delivered" || hits.Load() != 1 {
		t.Fatalf("want inbox + login-keyed webhook delivered, got %+v (webhook hits %d)", recs, hits.Load())
	}
	inbox := store.NewNotificationRepo(db)
	if n, _ := inbox.CountByUser(ctx, benKey, store.InboxFilter{}); n != 1 {
		t.Fatalf("inbox rows under the platform id = %d, want 1", n)
	}
	if n, _ := inbox.CountByUser(ctx, benLogin, store.InboxFilter{}); n != 0 {
		t.Fatalf("inbox rows under the login id = %d, want 0", n)
	}
}

func TestDispatcher_CategoryMuteUnderPlatformIDSuppresses(t *testing.T) {
	ctx := t.Context()
	d, db, hits := newWebhookDispatcher(t, benLogin)
	if err := store.NewCategoryPreferenceRepo(db).SetCategory(ctx, benKey, CategoryAgentMessage, false); err != nil {
		t.Fatalf("mute: %v", err)
	}

	recs := d.Deliver(ctx, a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Category: CategoryAgentMessage, Message: "m",
	})

	if len(recs) != 0 || hits.Load() != 0 {
		t.Fatalf("a mute under the platform id must suppress delivery, got %+v (webhook hits %d)", recs, hits.Load())
	}
}

func TestDispatcher_DedupDeliversOncePerRecipient(t *testing.T) {
	ctx := t.Context()
	d, db, hits := newWebhookDispatcher(t, benLogin)
	req := a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Message: "m", Dedup: "budget:mesh:2026-10:warn",
	}

	first := d.Deliver(ctx, req)
	req.RequestID = "r2"
	second := d.Deliver(ctx, req)

	if summarizeDeliveries(first) != "delivered" {
		t.Fatalf("first delivery = %+v", first)
	}
	if summarizeDeliveries(second) != "skipped" {
		t.Fatalf("a repeated dedup key must deliver nothing, got %+v", second)
	}
	if hits.Load() != 1 {
		t.Fatalf("webhook hits = %d, want 1", hits.Load())
	}
	if n, _ := store.NewNotificationRepo(db).CountByUser(ctx, benKey, store.InboxFilter{}); n != 1 {
		t.Fatalf("inbox rows = %d, want 1", n)
	}
}

func TestDispatcher_ARedeliveredRequestWritesOneRowAndSendsEachChannelOnce(t *testing.T) {
	ctx := t.Context()
	d, db, hits := newWebhookDispatcher(t, benLogin)
	req := a2a.NotifyRequest{RequestID: "r1", RecipientUserID: benLogin, Mode: a2a.NotifyModeNotify, Message: "m"}

	d.Deliver(ctx, req)
	again := d.Deliver(ctx, req)

	if summarizeDeliveries(again) != "skipped" {
		t.Fatalf("redelivery = %+v, want every channel skipped", again)
	}
	if hits.Load() != 1 {
		t.Errorf("webhook hits = %d, want 1", hits.Load())
	}
	if n, _ := store.NewNotificationRepo(db).CountByUser(ctx, benLogin, store.InboxFilter{}); n != 1 {
		t.Errorf("inbox rows = %d, want 1", n)
	}
}

func TestDispatcher_ARedeliverySendsTheChannelsTheFirstDeliveryMissed(t *testing.T) {
	ctx := t.Context()
	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(int(status.Load()))
	}))
	t.Cleanup(srv.Close)
	d, db, _ := newWebhookDispatcher(t, benLogin)
	d.dir = newConfigDirectory(map[string][]Channel{benLogin: {{Type: "webhook", Config: map[string]any{"url": srv.URL}}}})
	d.httpClient = srv.Client()
	req := a2a.NotifyRequest{RequestID: "r1", RecipientUserID: benLogin, Mode: a2a.NotifyModeNotify, Message: "m"}

	d.Deliver(ctx, req)
	status.Store(http.StatusOK)
	again := d.Deliver(ctx, req)
	third := d.Deliver(ctx, req)

	if summarizeDeliveries(again) != "delivered" {
		t.Fatalf("redelivery = %+v, want the missed webhook sent", again)
	}
	if summarizeDeliveries(third) != "skipped" {
		t.Errorf("third delivery = %+v, want every channel skipped", third)
	}
	if hits.Load() != 2 {
		t.Errorf("webhook hits = %d, want the failed send and one resend", hits.Load())
	}
	if n, _ := store.NewNotificationRepo(db).CountByUser(ctx, benLogin, store.InboxFilter{}); n != 1 {
		t.Errorf("inbox rows = %d, want 1", n)
	}
}

type channelFailingConnector struct {
	failChannel string
	sent        []string
}

func (c *channelFailingConnector) Invoke(_ context.Context, _, _ string, args map[string]any, _, _ string) (map[string]any, error) {
	ch, _ := args["channel"].(string)
	if ch == c.failChannel {
		return nil, errors.New("connector unavailable")
	}
	c.sent = append(c.sent, ch)
	return map[string]any{"success": true}, nil
}

func TestDispatcher_ARedeliverySendsTheConnectorDestinationTheFirstDeliveryMissed(t *testing.T) {
	ctx := t.Context()
	d, _, _ := newWebhookDispatcher(t, benLogin)
	slackChannel := func(id string) Channel {
		return Channel{Type: "connector", Config: map[string]any{
			"tool_name": "slack_send_message_990e8400", "str_binary": "slack", "args": map[string]any{"channel": id},
		}}
	}
	d.dir = newConfigDirectory(map[string][]Channel{benLogin: {slackChannel("C1"), slackChannel("C2")}})
	conn := &channelFailingConnector{failChannel: "C2"}
	d.SetConnectorInvoker(conn)
	req := a2a.NotifyRequest{RequestID: "r1", RecipientUserID: benLogin, Mode: a2a.NotifyModeNotify, Channels: []string{"connector"}, Message: "m"}

	d.Deliver(ctx, req)
	conn.failChannel = ""
	d.Deliver(ctx, req)

	if want := []string{"C1", "C2"}; !slices.Equal(conn.sent, want) {
		t.Fatalf("connector sends = %v, want %v: each destination once, the missed one on redelivery", conn.sent, want)
	}
}

func TestDispatcher_TwoWebhooksToOneURLAreEachSentOnce(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	var routes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		routes = append(routes, r.Header.Get("X-Route"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	d, _, _ := newWebhookDispatcher(t, benLogin)
	routed := func(route string) Channel {
		return Channel{Type: "webhook", Config: map[string]any{"url": srv.URL, "headers": map[string]any{"X-Route": route}}}
	}
	d.dir = newConfigDirectory(map[string][]Channel{benLogin: {routed("a"), routed("b")}})
	d.httpClient = srv.Client()
	req := a2a.NotifyRequest{RequestID: "r1", RecipientUserID: benLogin, Mode: a2a.NotifyModeNotify, Channels: []string{"webhook"}, Message: "m"}

	d.Deliver(ctx, req)
	d.Deliver(ctx, req)

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"a", "b"}; !slices.Equal(routes, want) {
		t.Fatalf("webhook routes hit = %v, want %v: each webhook once across a redelivery", routes, want)
	}
}

func TestDispatcher_DedupHoldsWithoutAnInboxRow(t *testing.T) {
	ctx := t.Context()
	d, _, hits := newWebhookDispatcher(t, benLogin)
	req := a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, Mode: a2a.NotifyModeNotify,
		Channels: []string{"webhook"}, Message: "m", Dedup: "k",
	}

	d.Deliver(ctx, req)
	if again := d.Deliver(ctx, req); summarizeDeliveries(again) != "skipped" {
		t.Fatalf("a repeated dedup key must deliver nothing, got %+v", again)
	}
	if hits.Load() != 1 {
		t.Fatalf("webhook hits = %d, want 1", hits.Load())
	}
}

func TestNotifyCore_LoginAddressedRowsAppearOnEveryRequest(t *testing.T) {
	ctx := t.Context()
	core := newIdentityTestCore(t, ben)
	loginOnly := func(id string) a2a.NotifyRequest {
		return a2a.NotifyRequest{RequestID: id, RecipientUserID: benLogin, Mode: a2a.NotifyModeNotify, Title: id, Message: "m"}
	}

	core.dispatcher.Deliver(ctx, loginOnly("before-first-visit"))
	if got := listInbox(t, core); len(got) != 1 || got[0].Title != "before-first-visit" {
		t.Fatalf("first visit inbox = %+v, want the login-addressed row", got)
	}

	core.dispatcher.Deliver(ctx, loginOnly("after-first-visit"))
	if got := listInbox(t, core); len(got) != 2 {
		t.Fatalf("second visit inbox = %+v, want both login-addressed rows", got)
	}
	if has, _ := core.db.HasRowsFor(ctx, benLogin); has {
		t.Fatal("login-keyed rows must all have moved under the platform id")
	}
}

func TestNotifyCore_LoginMuteCarriesOverToPlatformID(t *testing.T) {
	ctx := t.Context()
	core := newIdentityTestCore(t, ben)
	if err := core.categoryPrefs.SetCategory(ctx, benLogin, CategoryScheduledTask, false); err != nil {
		t.Fatalf("seed login mute: %v", err)
	}

	listInbox(t, core)
	core.dispatcher.Deliver(ctx, a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Category: CategoryScheduledTask, Message: "m",
	})

	if got := listInbox(t, core); len(got) != 0 {
		t.Fatalf("a mute set before the move must still apply, inbox = %+v", got)
	}
}

func TestNotifyCore_SubmitToOwnPlatformIDNeedsNoSendOthers(t *testing.T) {
	core := newIdentityTestCore(t, ben)

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify",
		strings.NewReader(`{"recipientUserId":"`+benKey+`","message":"note to self"}`)))

	if rr.Code != http.StatusOK {
		t.Fatalf("self-notify by platform id = %d, body=%s", rr.Code, rr.Body.String())
	}
	if got := listInbox(t, core); len(got) != 1 || got[0].Body != "note to self" {
		t.Fatalf("inbox = %+v, want the self-notification", got)
	}
}

func TestNotifyCore_SubmitToAnotherUserStillNeedsSendOthers(t *testing.T) {
	core := newIdentityTestCore(t, ben)

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/notify",
		strings.NewReader(`{"recipientUserId":"someone@example.com","message":"hi"}`)))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("notifying another user without send-others = %d, want 403", rr.Code)
	}
}

func TestNotifyCore_ScopeGatedCategoryOfferedOnlyToHolders(t *testing.T) {
	const gated = "test_admin_alerts"
	saved := categoryCatalog
	categoryCatalog = append(append([]Category(nil), saved...), Category{
		Key: gated, Label: "Admin alerts", GatesInbox: true, RequiresAnyScope: []string{"budget:*:update", "budget:*:assign"},
	})
	t.Cleanup(func() { categoryCatalog = saved })

	listed := func(core *NotifyCore) bool {
		rr := httptest.NewRecorder()
		core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/preferences/categories", nil))
		var page PaginatedResponse[CategoryPreferenceDTO]
		_ = json.Unmarshal(rr.Body.Bytes(), &page)
		for _, c := range page.Data {
			if c.Category == gated {
				return true
			}
		}
		return false
	}
	set := func(core *NotifyCore) int {
		rr := httptest.NewRecorder()
		core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences/categories",
			strings.NewReader(`{"category":"`+gated+`","enabled":false}`)))
		return rr.Code
	}

	holder := newIdentityTestCore(t, auth.NewTestSubject(auth.SubjectFields{UserID: "admin@example.com", Scopes: []string{"budget:*:assign"}}))
	if !listed(holder) || set(holder) != http.StatusOK {
		t.Fatal("a holder of one of the scopes must be offered and allowed to set the category")
	}
	regular := newIdentityTestCore(t, auth.NewTestSubject(auth.SubjectFields{UserID: "user@example.com", Scopes: []string{"agent:*:invoke"}}))
	if listed(regular) {
		t.Fatal("the category must not be listed to a caller without the scopes")
	}
	if code := set(regular); code != http.StatusBadRequest {
		t.Fatalf("setting the category without the scopes = %d, want 400 (as for an unknown category)", code)
	}
}

func getJSON[T any](t *testing.T, core *NotifyCore, path string) (int, PaginatedResponse[T]) {
	t.Helper()
	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	var page PaginatedResponse[T]
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return rr.Code, page
}

func TestNotifyCore_PreferencesListLoginKeyedDestinationsForPlatformCaller(t *testing.T) {
	core := newIdentityTestCoreWith(t, ben, map[string]any{"notify": map[string]any{"directory": []any{
		map[string]any{"user_id": benLogin, "channels": []any{
			map[string]any{"type": "webhook", "config": map[string]any{"url": "https://hooks.example.com/ben"}},
		}},
	}}})
	confirmChannel(t, core, benLogin, "slack", `{"user_id":"U1"}`)

	code, page := getJSON[PreferenceDTO](t, core, "/api/v1/notify/preferences")

	if code != http.StatusOK {
		t.Fatalf("GET preferences = %d", code)
	}
	types := map[string]bool{}
	for _, p := range page.Data {
		types[p.ChannelType] = true
	}
	if !types["webhook"] || !types["slack"] {
		t.Fatalf("preferences = %+v, want the login-keyed webhook and the learned slack channel", page.Data)
	}
}

type repoLearnedChannels struct{ repo *store.UserChannelRepo }

func (l repoLearnedChannels) ChannelsFor(ctx context.Context, userID string) ([]LearnedChannel, error) {
	rows, err := l.repo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	learned := make([]LearnedChannel, 0, len(rows))
	for _, uc := range rows {
		lc := LearnedChannel{ChannelType: uc.ChannelType}
		if uc.Confirmed() {
			lc.Address, lc.Status = uc.ConfirmedAddress, AddressConfirmed
		}
		learned = append(learned, lc)
	}
	return learned, nil
}

func confirmStoreChannel(t *testing.T, repo *store.UserChannelRepo, userID, channelType, address string) {
	t.Helper()
	if err := repo.Upsert(t.Context(), store.UserChannel{UserID: userID, ChannelType: channelType, Address: address}); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	hash := hashVerificationCode(userID, channelType, "ABCD-EFGH")
	if err := repo.StartConfirmation(t.Context(), userID, channelType, address, hash, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("start confirmation: %v", err)
	}
	if outcome, err := repo.ConfirmByCode(t.Context(), userID, channelType, hash, maxVerificationAttempts); err != nil || outcome != store.ConfirmOK {
		t.Fatalf("confirm by code: outcome=%v err=%v", outcome, err)
	}
}

func TestDispatcher_ProactiveReachesAddressLearnedUnderLoginID(t *testing.T) {
	ctx := t.Context()
	d, db, _ := newWebhookDispatcher(t, "nobody@example.com")
	chans := store.NewUserChannelRepo(db)
	confirmStoreChannel(t, chans, benLogin, "slack", `{"user_id":"U1"}`)
	pub := &fakeProactivePub{}
	d.SetProactive(pub, repoLearnedChannels{repo: chans}, nothingAdvertised)

	recs := d.Deliver(ctx, a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Message: "m",
	})

	if len(pub.calls) != 1 || string(pub.calls[0].Address) != `{"user_id":"U1"}` {
		t.Fatalf("proactive delivers = %+v, want one to the login-learned slack address", pub.calls)
	}
	if countRecs(recs, "slack", "sent") != 1 {
		t.Fatalf("records = %+v, want slack sent", recs)
	}
}

func TestNotifyCore_UserIDOverrideMatchesPlatformIDCaseInsensitively(t *testing.T) {
	ctx := t.Context()
	self := newIdentityTestCore(t, ben)
	if _, err := self.inbox.Add(ctx, benKey, "mine", "b", nil, "test", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	code, page := getJSON[NotificationDTO](t, self, "/api/v1/notify/inbox?userId="+benUUID)
	if code != http.StatusOK || len(page.Data) != 1 {
		t.Fatalf("own uppercase users.id = %d %+v, want 200 with own row and no others scope", code, page.Data)
	}

	const otherUUID = "0192F3A4-0000-7000-8000-0000000000AA"
	admin := newIdentityTestCore(t, auth.NewTestSubject(auth.SubjectFields{UserID: "admin@example.com", Scopes: []string{rbac.ScopeNotifyReadOthers}}))
	if _, err := admin.inbox.Add(ctx, strings.ToLower(otherUUID), "theirs", "b", nil, "test", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	code, page = getJSON[NotificationDTO](t, admin, "/api/v1/notify/inbox?userId="+otherUUID)
	if code != http.StatusOK || len(page.Data) != 1 || page.Data[0].Title != "theirs" {
		t.Fatalf("admin read of uppercase users.id = %d %+v, want the lowercase-keyed row", code, page.Data)
	}
}

func TestHTTP_ChannelConfirmByPlatformCallerConfirmsAChannelSeenUnderTheLoginID(t *testing.T) {
	core := newIdentityTestCore(t, ben)
	sink := deliverSink(t, core.svc.Broker(), "confirm-sink", core.cfg.Namespace, "slack")
	candidate := `{"channel_id":"C1","user_id":"U1"}`
	seedPresence(t, core, benLogin, "slack", candidate)

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if d := sink(2 * time.Second); d == nil || string(d.Address) != candidate {
		t.Fatalf("turned-on notice = %+v, want one sent to the candidate address", d)
	}

	if addr := confirmedAddress(t, core, benKey, "slack"); addr != candidate {
		t.Fatalf("confirmed address under the platform id = %q, want the candidate", addr)
	}
}

func TestDispatcher_DedupDoesNotApplyToInputMode(t *testing.T) {
	ctx := t.Context()
	d, db, _ := newWebhookDispatcher(t, "nobody@example.com")
	req := a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeInput, Message: "approve?", Dedup: "k",
	}

	d.Deliver(ctx, req)
	req.RequestID = "r2"
	d.Deliver(ctx, req)

	if n, _ := store.NewNotificationRepo(db).CountByUser(ctx, benKey, store.InboxFilter{}); n != 2 {
		t.Fatalf("input-mode inbox rows = %d, want 2 (dedup is the pending store's job)", n)
	}
}

func TestDispatcher_MutedSendLeavesDedupKeyUnclaimed(t *testing.T) {
	ctx := t.Context()
	d, db, _ := newWebhookDispatcher(t, "nobody@example.com")
	cats := store.NewCategoryPreferenceRepo(db)
	req := a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Category: CategoryAgentMessage, Message: "m", Dedup: "k",
	}
	if err := cats.SetCategory(ctx, benKey, CategoryAgentMessage, false); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if recs := d.Deliver(ctx, req); recs != nil {
		t.Fatalf("muted send = %+v, want nothing", recs)
	}
	if err := cats.SetCategory(ctx, benKey, CategoryAgentMessage, true); err != nil {
		t.Fatalf("unmute: %v", err)
	}

	req.RequestID = "r2"
	if recs := d.Deliver(ctx, req); summarizeDeliveries(recs) != "delivered" {
		t.Fatalf("send after unmute = %+v, want delivered", recs)
	}
}

func TestDispatcher_AliasLookupFailureKeepsTheInboxAndSkipsOutOfBand(t *testing.T) {
	ctx := t.Context()
	d, db, hits := newWebhookDispatcher(t, benLogin)
	d.SetAliases(db)
	if _, err := db.ExecContext(ctx, `DROP TABLE notify_user_aliases`); err != nil {
		t.Fatalf("break alias store: %v", err)
	}

	recs := d.Deliver(ctx, loginOnlyRequest("r1"))

	if hits.Load() != 0 {
		t.Fatalf("webhook hits = %d, want 0: an opt-out under the unresolved users.id may apply", hits.Load())
	}
	if countRecs(recs, inboxChannelType, "sent") != 1 {
		t.Fatalf("records = %+v, want the inbox row written", recs)
	}
	if n, _ := store.NewNotificationRepo(db).CountByUser(ctx, benLogin, store.InboxFilter{}); n != 1 {
		t.Fatalf("inbox rows under the login id = %d, want 1", n)
	}
}

type flakyPreferences struct {
	Preferences
	failNext atomic.Bool
}

func (p *flakyPreferences) DisabledChannels(ctx context.Context, userID string) (map[string]bool, error) {
	if p.failNext.CompareAndSwap(true, false) {
		return nil, errors.New("preferences store unavailable")
	}
	return p.Preferences.DisabledChannels(ctx, userID)
}

func TestDispatcher_DedupKeyWithoutAnInboxRowSurvivesALookupFailure(t *testing.T) {
	ctx := t.Context()
	d, db, hits := newWebhookDispatcher(t, benLogin)
	prefs := &flakyPreferences{Preferences: store.NewPreferenceRepo(db)}
	prefs.failNext.Store(true)
	d.SetPreferences(prefs)
	req := a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, Mode: a2a.NotifyModeNotify,
		Channels: []string{"webhook"}, Message: "m", Dedup: "k",
	}

	if recs := d.Deliver(ctx, req); summarizeDeliveries(recs) != "skipped" {
		t.Fatalf("send while preferences fail = %+v, want skipped", recs)
	}
	req.RequestID = "r2"
	if recs := d.Deliver(ctx, req); summarizeDeliveries(recs) != "delivered" {
		t.Fatalf("retry with the same key = %+v, want delivered", recs)
	}
	if hits.Load() != 1 {
		t.Fatalf("webhook hits = %d, want 1", hits.Load())
	}
}

func TestHTTP_ChannelConfirmSeesPresenceRecordedAfterTheFirstRequest(t *testing.T) {
	core := newIdentityTestCore(t, ben)
	sink := deliverSink(t, core.svc.Broker(), "confirm-sink", core.cfg.Namespace, "slack")
	listInbox(t, core)
	candidate := `{"channel_id":"C1","user_id":"U1"}`
	seedPresence(t, core, benLogin, "slack", candidate)

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if d := sink(2 * time.Second); d == nil || string(d.Address) != candidate {
		t.Fatalf("turned-on notice = %+v, want one sent to the candidate address", d)
	}
	if addr := confirmedAddress(t, core, benKey, "slack"); addr != candidate {
		t.Fatalf("confirmed address under the platform id = %q, want the candidate", addr)
	}
}

func TestHTTP_ChannelListShowsAChannelConfirmedUnderTheLoginIDAfterTheFirstRequest(t *testing.T) {
	core := newIdentityTestCore(t, ben)
	listInbox(t, core)
	confirmChannel(t, core, benLogin, "slack", `{"user_id":"U1"}`)

	code, page := getJSON[UserChannelDTO](t, core, "/api/v1/notify/channels")

	if code != http.StatusOK || len(page.Data) != 1 || page.Data[0].ChannelType != "slack" || !page.Data[0].Confirmed {
		t.Fatalf("GET channels = %d %+v, want the login-keyed confirmed slack channel", code, page.Data)
	}
}

func TestNotifyCore_AdminOverrideByLoginIDReachesTheTargetsDirectoryChannels(t *testing.T) {
	const hook = "https://hooks.example.com/ben"
	admin := auth.NewTestSubject(auth.SubjectFields{UserID: "admin@example.com", Scopes: []string{rbac.ScopeNotifyReadOthers, rbac.ScopeNotifySendOthers}})
	core := newIdentityTestCoreWith(t, admin, map[string]any{"notify": map[string]any{"directory": []any{
		map[string]any{"user_id": benLogin, "channels": []any{
			map[string]any{"type": "webhook", "config": map[string]any{"url": hook}},
		}},
	}}})
	if err := core.db.RecordAlias(t.Context(), benLogin, benKey); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	webhookKey := destinationKey(Channel{Type: "webhook", Config: map[string]any{"url": hook}})

	if _, page := getJSON[PreferenceDTO](t, core, "/api/v1/notify/preferences?userId="+benLogin); len(page.Data) != 1 || page.Data[0].ChannelType != "webhook" {
		t.Fatalf("preferences by login id = %+v, want the target's directory webhook", page.Data)
	}
	rr := do(core, http.MethodGet, "/api/v1/notify/preferences/matrix?userId="+benLogin, "")
	var matrix NotificationMatrixDTO
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &matrix) != nil || len(matrix.Channels) != 2 {
		t.Fatalf("matrix by login id = %d %s, want the inbox and the directory webhook", rr.Code, rr.Body.String())
	}
	put := `{"cells":[{"category":"` + CategoryAgentMessage + `","channelType":"webhook","destinationKey":"` + webhookKey + `","enabled":false}]}`
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences/matrix?userId="+benLogin, put); rr.Code != http.StatusOK {
		t.Fatalf("matrix PUT for the directory webhook = %d %s, want 200", rr.Code, rr.Body.String())
	}
	cells, err := store.NewCellPreferenceRepo(core.db).DisabledCells(t.Context(), benKey)
	if err != nil || len(cells) != 1 {
		t.Fatalf("cells under the target's users.id = %v, %v; want the webhook cell off", cells, err)
	}
}

func TestNotifyCore_InboxCategoryFilterHidesAGatedCategoryFromANonHolder(t *testing.T) {
	const gated = "test_admin_alerts"
	saved := categoryCatalog
	categoryCatalog = append(append([]Category(nil), saved...), Category{
		Key: gated, Label: "Admin alerts", GatesInbox: true, RequiresAnyScope: []string{"budget:*:update"},
	})
	t.Cleanup(func() { categoryCatalog = saved })
	statuses := func(core *NotifyCore, category string) (list, readAll int) {
		list = do(core, http.MethodGet, "/api/v1/notify/inbox?category="+category, "").Code
		readAll = do(core, http.MethodPost, "/api/v1/notify/inbox/read-all?category="+category, "").Code
		return list, readAll
	}

	holder := newIdentityTestCore(t, auth.NewTestSubject(auth.SubjectFields{UserID: "admin@example.com", Scopes: []string{"budget:*:update"}}))
	if list, readAll := statuses(holder, gated); list != http.StatusOK || readAll != http.StatusNoContent {
		t.Fatalf("holder filter = %d / %d, want 200 / 204", list, readAll)
	}
	regular := newIdentityTestCore(t, auth.NewTestSubject(auth.SubjectFields{UserID: "user@example.com"}))
	gatedList, gatedReadAll := statuses(regular, gated)
	unknownList, unknownReadAll := statuses(regular, "no_such_category")
	if gatedList != http.StatusBadRequest || gatedReadAll != http.StatusBadRequest || gatedList != unknownList || gatedReadAll != unknownReadAll {
		t.Fatalf("non-holder filter = %d / %d, want 400 / 400 like an unknown category (%d / %d)",
			gatedList, gatedReadAll, unknownList, unknownReadAll)
	}
}
