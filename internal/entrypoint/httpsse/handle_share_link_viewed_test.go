// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

const (
	shareOwner     = "owner@example.com"
	shareRecipient = "recipient@example.com"
)

func shareWithUser(t *testing.T, mux *http.ServeMux, sessionID, email, level string) string {
	t.Helper()
	share := createShareAs(t, mux, sessionID, shareOwner, shareOwner, "")
	addShareUser(t, mux, share.ID, email, level)
	return share.ID
}

func addShareUser(t *testing.T, mux *http.ServeMux, shareID, email, level string) {
	t.Helper()
	body := `{"shares":[{"userEmail":"` + email + `","accessLevel":"` + level + `"}]}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser("POST", "/api/v1/share/"+shareID+"/users", body, shareOwner, shareOwner))
	if rec.Code != http.StatusOK {
		t.Fatalf("add share user: got %d: %s", rec.Code, rec.Body.String())
	}
}

func sharedWithMe(t *testing.T, mux *http.ServeMux, email string) []SharedWithMeItem {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser("GET", "/api/v1/share/sharedWithMe", "", email, email))
	if rec.Code != http.StatusOK {
		t.Fatalf("sharedWithMe: got %d: %s", rec.Code, rec.Body.String())
	}
	var page PaginatedResponse[SharedWithMeItem]
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode sharedWithMe: %v", err)
	}
	return page.Data
}

func itemByID(t *testing.T, items []SharedWithMeItem, shareID string) SharedWithMeItem {
	t.Helper()
	for _, it := range items {
		if it.ID == shareID {
			return it
		}
	}
	t.Fatalf("share %s not present in %+v", shareID, items)
	return SharedWithMeItem{}
}

func markViewed(t *testing.T, mux *http.ServeMux, shareID, email string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser("POST", "/api/v1/share/"+shareID+"/viewed", "", email, email))
	return rec
}

func TestShareLink_SharedWithMeExposesViewedAt(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000000101", "Q3 planning")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000000101", shareRecipient, "resource_viewer")

	before := itemByID(t, sharedWithMe(t, mux, shareRecipient), shareID)
	if before.ViewedAt != nil {
		t.Fatalf("a new share must be unopened, got viewedAt=%v", *before.ViewedAt)
	}

	if rec := markViewed(t, mux, shareID, shareRecipient); rec.Code != http.StatusOK {
		t.Fatalf("mark viewed: got %d: %s", rec.Code, rec.Body.String())
	}

	after := itemByID(t, sharedWithMe(t, mux, shareRecipient), shareID)
	if after.ViewedAt == nil {
		t.Fatal("viewedAt must be set after the recipient opens the share")
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000000+00:00", *after.ViewedAt); err != nil {
		t.Fatalf("viewedAt must be ISO 8601, got %q: %v", *after.ViewedAt, err)
	}
}

func TestShareLink_SharedWithMeOrdersMostRecentFirst(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000000201", "older")
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000000202", "newer")

	first := shareWithUser(t, mux, "00000000-0000-0000-0000-000000000201", shareRecipient, "resource_viewer")
	time.Sleep(2 * time.Millisecond)
	second := shareWithUser(t, mux, "00000000-0000-0000-0000-000000000202", shareRecipient, "resource_viewer")

	items := sharedWithMe(t, mux, shareRecipient)
	if len(items) != 2 {
		t.Fatalf("expected 2 shares, got %+v", items)
	}
	if items[0].ID != second || items[1].ID != first {
		t.Fatalf("most recently shared must be first: got [%s %s], want [%s %s]",
			items[0].ID, items[1].ID, second, first)
	}
}

func TestShareLink_MarkViewedRejectsNonRecipients(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000000301", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000000301", shareRecipient, "resource_viewer")

	for _, who := range []string{shareOwner, "stranger@example.com"} {
		t.Run(who, func(t *testing.T) {
			if rec := markViewed(t, mux, shareID, who); rec.Code != http.StatusNotFound {
				t.Fatalf("got %d, want 404: %s", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("unknown share", func(t *testing.T) {
		if rec := markViewed(t, mux, "no-such-share-id-000", shareRecipient); rec.Code != http.StatusNotFound {
			t.Fatalf("got %d, want 404", rec.Code)
		}
	})
}

func TestShareLink_MarkViewedRequiresIdentity(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000000401", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000000401", shareRecipient, "resource_viewer")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/share/"+shareID+"/viewed", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestShareLink_MarkViewedIsIdempotent(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000000501", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000000501", shareRecipient, "resource_viewer")

	first := markViewed(t, mux, shareID, shareRecipient)
	if first.Code != http.StatusOK {
		t.Fatalf("first mark viewed: got %d", first.Code)
	}
	second := markViewed(t, mux, shareID, shareRecipient)
	if second.Code != http.StatusOK {
		t.Fatalf("re-marking an already-read share must stay 200, got %d: %s", second.Code, second.Body.String())
	}
}

func TestShareLink_MarkViewedDoesNotAffectOtherRecipient(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000000601", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000000601", shareRecipient, "resource_viewer")
	addShareUser(t, mux, shareID, "second@example.com", "resource_viewer")

	if rec := markViewed(t, mux, shareID, shareRecipient); rec.Code != http.StatusOK {
		t.Fatalf("mark viewed: got %d", rec.Code)
	}

	if itemByID(t, sharedWithMe(t, mux, shareRecipient), shareID).ViewedAt == nil {
		t.Fatal("the recipient who opened the share must be marked read")
	}
	if itemByID(t, sharedWithMe(t, mux, "second@example.com"), shareID).ViewedAt != nil {
		t.Fatal("the other recipient must still be unread")
	}
}

func TestShareLink_SelfSnapshotRefreshStaysRead(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000000701", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000000701", shareRecipient, "resource_viewer")

	if rec := markViewed(t, mux, shareID, shareRecipient); rec.Code != http.StatusOK {
		t.Fatalf("mark viewed: got %d", rec.Code)
	}
	time.Sleep(3 * time.Millisecond)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser("POST", "/api/v1/share/"+shareID+"/updateSnapshot", "{}", shareRecipient, shareRecipient))
	if rec.Code != http.StatusOK {
		t.Fatalf("self updateSnapshot: got %d: %s", rec.Code, rec.Body.String())
	}

	item := itemByID(t, sharedWithMe(t, mux, shareRecipient), shareID)
	if item.ViewedAt == nil {
		t.Fatal("catching up must not clear viewedAt")
	}
	if toMillis(t, *item.ViewedAt) < toMillis(t, item.SharedAt) {
		t.Fatalf("share must still read as read after a self refresh: viewedAt=%s sharedAt=%s", *item.ViewedAt, item.SharedAt)
	}
}

func TestShareLink_OwnerSnapshotRefreshRemarksUnread(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000000801", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000000801", shareRecipient, "resource_viewer")

	if rec := markViewed(t, mux, shareID, shareRecipient); rec.Code != http.StatusOK {
		t.Fatalf("mark viewed: got %d", rec.Code)
	}
	time.Sleep(2 * time.Millisecond)

	body := `{"userEmail":"` + shareRecipient + `"}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser("POST", "/api/v1/share/"+shareID+"/updateSnapshot", body, shareOwner, shareOwner))
	if rec.Code != http.StatusOK {
		t.Fatalf("owner updateSnapshot: got %d: %s", rec.Code, rec.Body.String())
	}

	item := itemByID(t, sharedWithMe(t, mux, shareRecipient), shareID)
	if item.ViewedAt != nil {
		t.Fatalf("owner advancing the snapshot must clear the read stamp, got viewedAt=%s", *item.ViewedAt)
	}
}

func toMillis(t *testing.T, iso string) int64 {
	t.Helper()
	parsed, err := time.Parse("2006-01-02T15:04:05.000000+00:00", iso)
	if err != nil {
		t.Fatalf("parse %q: %v", iso, err)
	}
	return parsed.UnixMilli()
}

func TestShareLink_SharedWithMePaginates(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	for i, sid := range []string{
		"00000000-0000-0000-0000-000000000901",
		"00000000-0000-0000-0000-000000000902",
	} {
		seedSession(t, sessRepo, shareOwner, sid, "chat")
		shareWithUser(t, mux, sid, shareRecipient, "resource_viewer")
		if i == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser("GET", "/api/v1/share/sharedWithMe?pageNumber=1&pageSize=1", "", shareRecipient, shareRecipient))
	if rec.Code != http.StatusOK {
		t.Fatalf("paginated sharedWithMe: got %d: %s", rec.Code, rec.Body.String())
	}
	var page PaginatedResponse[SharedWithMeItem]
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("pageSize=1 must return 1 item, got %d", len(page.Data))
	}
	if page.Meta.Pagination.Count != 2 || page.Meta.Pagination.TotalPages != 2 {
		t.Fatalf("pagination metadata must reflect the real total, got %+v", page.Meta.Pagination)
	}
}

func TestShareLink_SharedWithMeRejectsBadPagination(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser("GET", "/api/v1/share/sharedWithMe?pageSize=9999", "", shareRecipient, shareRecipient))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func setNotifyService(t *testing.T, h *shareLinkHandler, enabled bool) {
	t.Helper()
	reg := h.core.Services().FeatureRegistry()
	if reg == nil {
		t.Fatal("expected a feature registry in the test runtime")
	}
	if reg.HasEnvOverride(features.KeyNotifyService) {
		t.Skipf("SAM_FEATURE_NOTIFY_SERVICE is set in the environment and overrides the flag default")
	}
	def, ok := reg.Get(features.KeyNotifyService)
	if !ok {
		t.Fatalf("flag %q is not registered", features.KeyNotifyService)
	}
	def.Default = enabled
	reg.Register(def)
	if reg.IsEnabled(features.KeyNotifyService) != enabled {
		t.Fatalf("notify_service enabled = %v, want %v", !enabled, enabled)
	}
}

func subscribeNotifications(t *testing.T, h *shareLinkHandler, recipient string) (broker.Broker, broker.Queue) {
	t.Helper()
	brk := h.core.Services().Broker()
	if brk == nil {
		t.Fatal("expected a broker in the test runtime")
	}
	queue, err := brk.CreateQueue("test-notify-"+recipient, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	t.Cleanup(func() { _ = brk.DeleteQueue(queue.Name()) })
	if err := brk.Subscribe(queue, a2a.NotifyRequestTopic("test-ns", recipient)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return brk, queue
}

func receiveNotify(t *testing.T, brk broker.Broker, queue broker.Queue) (a2a.NotifyRequest, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	msg, err := brk.Receive(ctx, queue)
	if err != nil || msg == nil {
		return a2a.NotifyRequest{}, false
	}
	var req a2a.NotifyRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatalf("unmarshal notify request: %v", err)
	}
	return req, true
}

func TestShareLink_AddUsersPublishesNotificationWhenEnabled(t *testing.T) {
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	setNotifyService(t, h, true)
	brk, queue := subscribeNotifications(t, h, shareRecipient)

	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000001001", "Q3 planning")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000001001", shareRecipient, "resource_viewer")

	req, ok := receiveNotify(t, brk, queue)
	if !ok {
		t.Fatal("expected a notification for the new recipient")
	}
	if req.RecipientUserID != shareRecipient {
		t.Errorf("recipient = %q, want %q", req.RecipientUserID, shareRecipient)
	}
	if req.Mode != a2a.NotifyModeNotify {
		t.Errorf("mode = %q, want %q", req.Mode, a2a.NotifyModeNotify)
	}
	if req.Category != a2a.CategoryChatShare {
		t.Errorf("category = %q, want %q (so users can opt out of share notices alone)", req.Category, a2a.CategoryChatShare)
	}
	if req.SenderAgent != notifySourceChatShare {
		t.Errorf("senderAgent = %q, want %q", req.SenderAgent, notifySourceChatShare)
	}
	if len(req.Channels) != 0 {
		t.Errorf("channels = %v, want empty", req.Channels)
	}
	if req.Metadata["share_id"] != shareID {
		t.Errorf("metadata share_id = %v, want %q", req.Metadata["share_id"], shareID)
	}
	shareURL, _ := req.Metadata["share_url"].(string)
	if shareURL == "" || !strings.Contains(req.Message, shareURL) {
		t.Errorf("message %q must contain the share URL %q", req.Message, shareURL)
	}
	if _, present := req.Metadata["session_id"]; present {
		t.Error("metadata must not carry session_id: it would produce a deep link the recipient cannot follow")
	}
}

func TestShareLink_AddUsersDoesNotNotifyWhenDisabled(t *testing.T) {
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	setNotifyService(t, h, false)
	brk, queue := subscribeNotifications(t, h, shareRecipient)

	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000001101", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000001101", shareRecipient, "resource_viewer")

	if _, ok := receiveNotify(t, brk, queue); ok {
		t.Fatal("no notification may be published while the notify service is disabled")
	}
	if itemByID(t, sharedWithMe(t, mux, shareRecipient), shareID).ViewedAt != nil {
		t.Fatal("the share must still be listed and unread with notifications off")
	}
}

func TestShareLink_AddUsersDoesNotRenotifyOnRepeatOrRoleChange(t *testing.T) {
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	setNotifyService(t, h, true)
	brk, queue := subscribeNotifications(t, h, shareRecipient)

	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000001201", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000001201", shareRecipient, "resource_viewer")
	if _, ok := receiveNotify(t, brk, queue); !ok {
		t.Fatal("expected the initial notification")
	}

	addShareUser(t, mux, shareID, shareRecipient, "resource_viewer")
	addShareUser(t, mux, shareID, shareRecipient, "resource_editor")

	if _, ok := receiveNotify(t, brk, queue); ok {
		t.Fatal("re-sharing or changing a role must not send a second notification")
	}
}

func TestShareLink_AddUsersDoesNotNotifySelf(t *testing.T) {
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	setNotifyService(t, h, true)
	brk, queue := subscribeNotifications(t, h, shareOwner)

	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000001301", "chat")
	share := createShareAs(t, mux, "00000000-0000-0000-0000-000000001301", shareOwner, shareOwner, "")
	addShareUser(t, mux, share.ID, shareOwner, "resource_viewer")

	if _, ok := receiveNotify(t, brk, queue); ok {
		t.Fatal("an owner adding themselves must not be notified about their own chat")
	}
}

func streamsOf(t *testing.T, h *shareLinkHandler) *SSEManager {
	t.Helper()
	p, ok := h.userNotifier.(*userEventPublisher)
	if !ok {
		t.Fatalf("share handler notifier is %T, want the user event publisher", h.userNotifier)
	}
	return p.sse
}

func TestShareLink_AddUsersSucceedsWithoutSSEManager(t *testing.T) {
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	h.userNotifier = nil

	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000001401", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000001401", shareRecipient, "resource_viewer")

	if itemByID(t, sharedWithMe(t, mux, shareRecipient), shareID).ID != shareID {
		t.Fatal("share must be created without an SSE manager")
	}
}

func TestShareLink_AddUsersPublishesChatSharedForTheOtherGateways(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)
	brk := adapter.core.Services().Broker()
	sub, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(sub, a2a.UserEventTopic("test-ns")); err != nil {
		t.Fatal(err)
	}
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000001601", "Q4 planning")

	share := createShareAs(t, adapter.Mux(), "00000000-0000-0000-0000-000000001601", shareOwner, shareOwner, "")
	req := reqWithUser("POST", "/api/v1/share/"+share.ID+"/users", `{"shares":[{"userEmail":"`+shareRecipient+`","accessLevel":"resource_viewer"}]}`, shareOwner, shareOwner)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	adapter.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add share user: got %d: %s", rec.Code, rec.Body.String())
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, sub)
	if err != nil {
		t.Fatalf("no user event published: %v", err)
	}
	var evt a2a.UserEvent
	if err := json.Unmarshal(msg.Payload, &evt); err != nil {
		t.Fatal(err)
	}
	if evt.Event != shareEventChatShared || len(evt.Keys) != 1 || evt.Keys[0] != shareRecipient {
		t.Errorf("user event = %+v, want %s for %s", evt, shareEventChatShared, shareRecipient)
	}
}

func TestShareLink_AddUsersPushesChatSharedEvent(t *testing.T) {
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	events, remove := streamsOf(t, h).AddUserNotificationQueue(shareRecipient)
	defer remove()

	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000001501", "Q3 planning")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000001501", shareRecipient, "resource_viewer")

	select {
	case evt := <-events:
		if evt.Event != shareEventChatShared {
			t.Fatalf("event = %q, want %q", evt.Event, shareEventChatShared)
		}
		var payload chatSharedEvent
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("unmarshal event data %q: %v", evt.Data, err)
		}
		if payload.ShareID != shareID {
			t.Errorf("shareId = %q, want %q", payload.ShareID, shareID)
		}
		if payload.Reason != shareReasonShared {
			t.Errorf("reason = %q, want %q", payload.Reason, shareReasonShared)
		}
		if payload.OwnerEmail != shareOwner {
			t.Errorf("ownerEmail = %q, want %q", payload.OwnerEmail, shareOwner)
		}
		if payload.SessionID != nil {
			t.Errorf("a viewer must not receive a sessionId, got %q", *payload.SessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a chat_shared event on the recipient's stream")
	}
}

type syncStreamRecorder struct {
	mu   sync.Mutex
	body strings.Builder
	hdr  http.Header
}

func newSyncStreamRecorder() *syncStreamRecorder {
	return &syncStreamRecorder{hdr: make(http.Header)}
}

func (s *syncStreamRecorder) Header() http.Header { return s.hdr }

func (s *syncStreamRecorder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body.Write(p)
}

func (s *syncStreamRecorder) WriteHeader(int) {}

func (s *syncStreamRecorder) Flush() {}

func (s *syncStreamRecorder) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body.String()
}

func awaitStream(t *testing.T, rec *syncStreamRecorder, want, whatFor string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if strings.Contains(rec.text(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: stream never contained %q; got %q", whatFor, want, rec.text())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestShareLink_PushReachesStreamRegisteredUnderCallerIdentity(t *testing.T) {
	cases := []struct {
		name           string
		subscriberID   string
		subscriberMail string
	}{
		{"identity is the lowercased email", shareRecipient, shareRecipient},
		{"identity is a differently-cased email", "ReCiPiEnT@Example.COM", "ReCiPiEnT@Example.COM"},
		{"identity is an opaque subject", "e6f1a0c2-0000-4000-8000-000000000001", shareRecipient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, mux, sessRepo, _ := setupShareLinkTest(t)
			notifH := &userNotificationsHandler{core: h.core, sseManager: streamsOf(t, h), logger: h.logger}
			mux.HandleFunc("GET /api/v1/sse/notifications", notifH.handleSubscribe)

			streamReq := reqWithUser("GET", "/api/v1/sse/notifications", "", tc.subscriberID, tc.subscriberMail)
			streamCtx, cancelStream := context.WithCancel(streamReq.Context())
			defer cancelStream()
			streamReq = streamReq.WithContext(streamCtx)
			streamRec := newSyncStreamRecorder()
			streamDone := make(chan struct{})
			go func() {
				defer close(streamDone)
				mux.ServeHTTP(streamRec, streamReq)
			}()
			awaitStream(t, streamRec, "notification stream established", "opening the stream")

			seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000001701", "Q3 planning")
			shareWithUser(t, mux, "00000000-0000-0000-0000-000000001701", shareRecipient, "resource_viewer")

			awaitStream(t, streamRec, shareEventChatShared, "recipient's live stream")
			cancelStream()
			<-streamDone
		})
	}
}

func TestShareLink_RoleChangePushesAccessChangedEvent(t *testing.T) {
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, shareOwner, "00000000-0000-0000-0000-000000001601", "chat")
	shareID := shareWithUser(t, mux, "00000000-0000-0000-0000-000000001601", shareRecipient, "resource_viewer")

	events, remove := streamsOf(t, h).AddUserNotificationQueue(shareRecipient)
	defer remove()
	addShareUser(t, mux, shareID, shareRecipient, "resource_editor")

	select {
	case evt := <-events:
		var payload chatSharedEvent
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("unmarshal event data: %v", err)
		}
		if payload.Reason != shareReasonAccessChanged {
			t.Fatalf("reason = %q, want %q", payload.Reason, shareReasonAccessChanged)
		}
		if payload.SessionID == nil || *payload.SessionID == "" {
			t.Error("an editor must receive the sessionId")
		}
	case <-time.After(time.Second):
		t.Fatal("expected a chat_shared event for the role change")
	}
}
