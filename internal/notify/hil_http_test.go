// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

func seedHILInboxRow(t *testing.T, core *NotifyCore, userID, requestID string) string {
	t.Helper()
	id, err := core.inbox.Add(t.Context(), userID, "Approve?", "approve the deploy", map[string]any{
		"mode":       a2a.NotifyModeInput,
		"request_id": requestID,
		"surface":    json.RawMessage(`{"surfaceId":"s1"}`),
	}, "agent-1", CategoryHIL)
	if err != nil {
		t.Fatalf("seed HIL inbox row: %v", err)
	}
	return id
}

func postRespond(t *testing.T, core *NotifyCore, id, userID, jsonBody string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notify/inbox/"+id+"/respond?userId="+userID, strings.NewReader(jsonBody))
	core.handler.ServeHTTP(rr, req)
	return rr
}

func TestInboxRespond_RoutesAnswerThroughRouter(t *testing.T) {
	core, brk, ns := startRouterCore(t)

	const requestID = "req-resp-1"
	respAddr := a2a.NotifyResponseTopic(ns, "agent-1", requestID)
	if err := core.pending.Insert(t.Context(), store.PendingRequest{
		RequestID: requestID, RequesterAddr: respAddr, RecipientUserID: "trent",
	}); err != nil {
		t.Fatalf("seed pending request: %v", err)
	}
	sink := requesterSink(t, brk, "resp-sink", respAddr)
	id := seedHILInboxRow(t, core, "trent", requestID)

	rr := postRespond(t, core, id, "trent", `{"status":"answered","data":{"choice":"approve"}}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("respond status = %d, body=%s", rr.Code, rr.Body.String())
	}

	got := sink(3 * time.Second)
	if got == nil || got.Status != "answered" || got.Data["choice"] != "approve" {
		t.Fatalf("answer was not routed to the requester: %+v", got)
	}
	if got.RequestID != requestID {
		t.Errorf("routed response request_id = %q, want %q", got.RequestID, requestID)
	}

	row, err := core.inbox.Get(t.Context(), "trent", id)
	if err != nil {
		t.Fatalf("Get inbox row: %v", err)
	}
	if row.Status != "read" {
		t.Errorf("inbox row status = %q, want read", row.Status)
	}
}

func TestInboxRespond_CancelledDropsData(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	const requestID = "req-cancel-1"
	respAddr := a2a.NotifyResponseTopic(ns, "agent-1", requestID)
	if err := core.pending.Insert(t.Context(), store.PendingRequest{
		RequestID: requestID, RequesterAddr: respAddr, RecipientUserID: "trent",
	}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	sink := requesterSink(t, brk, "cancel-sink", respAddr)
	id := seedHILInboxRow(t, core, "trent", requestID)

	rr := postRespond(t, core, id, "trent", `{"status":"cancelled","data":{"x":1}}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("cancel respond status = %d, body=%s", rr.Code, rr.Body.String())
	}
	got := sink(3 * time.Second)
	if got == nil || got.Status != "cancelled" {
		t.Fatalf("expected a cancelled response routed, got %+v", got)
	}
	if got.Data != nil {
		t.Errorf("a cancelled response must carry no data, got %v", got.Data)
	}
}

func TestInboxRespond_Guards(t *testing.T) {
	core, _, _ := startRouterCore(t)

	t.Run("non-HIL row rejected", func(t *testing.T) {
		id, err := core.inbox.Add(t.Context(), "trent", "FYI", "done", map[string]any{"task_id": "t1"}, "notify_user", "")
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		rr := postRespond(t, core, id, "trent", `{"status":"answered"}`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("respond to a non-input notification should be 400, got %d", rr.Code)
		}
	})

	t.Run("missing notification is 404", func(t *testing.T) {
		rr := postRespond(t, core, idgen.New(), "trent", `{"status":"answered"}`)
		if rr.Code != http.StatusNotFound {
			t.Errorf("respond to a missing notification should be 404, got %d", rr.Code)
		}
	})

	t.Run("invalid status rejected", func(t *testing.T) {
		id := seedHILInboxRow(t, core, "trent", "req-bad-status")
		rr := postRespond(t, core, id, "trent", `{"status":"maybe"}`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("invalid status should be 400, got %d", rr.Code)
		}
	})

	t.Run("another user's row is 404 (scoped)", func(t *testing.T) {
		id := seedHILInboxRow(t, core, "trent", "req-other-user")
		rr := postRespond(t, core, id, "bob", `{"status":"answered"}`)
		if rr.Code != http.StatusNotFound {
			t.Errorf("responding to another user's row should be 404, got %d", rr.Code)
		}
	})

	t.Run("malformed path id is 400", func(t *testing.T) {
		rr := postRespond(t, core, "not-a-uuid", "trent", `{"status":"answered"}`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("malformed id should be 400, got %d", rr.Code)
		}
	})

	t.Run("malformed body is 400", func(t *testing.T) {
		id := seedHILInboxRow(t, core, "trent", "req-bad-body")
		rr := postRespond(t, core, id, "trent", `{not json`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("malformed body should be 400, got %d", rr.Code)
		}
	})
}

func TestInboxRespond_BrokerDownIs500(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	const requestID = "req-broker-down"
	if err := core.pending.Insert(t.Context(), store.PendingRequest{
		RequestID: requestID, RequesterAddr: a2a.NotifyResponseTopic(ns, "agent-1", requestID), RecipientUserID: "trent",
	}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	id := seedHILInboxRow(t, core, "trent", requestID)
	_ = brk.Disconnect(context.Background())

	rr := postRespond(t, core, id, "trent", `{"status":"answered"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("respond with a down broker should be 500, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestInboxRespond_ResolvedRequestIs409(t *testing.T) {
	core, _, ns := startRouterCore(t)
	const requestID = "req-resolved"
	if err := core.pending.Insert(t.Context(), store.PendingRequest{
		RequestID: requestID, RequesterAddr: a2a.NotifyResponseTopic(ns, "agent-1", requestID), RecipientUserID: "trent",
	}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	if _, claimed, err := core.pending.Claim(t.Context(), requestID, "answered"); err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	id := seedHILInboxRow(t, core, "trent", requestID)

	rr := postRespond(t, core, id, "trent", `{"status":"answered"}`)
	if rr.Code != http.StatusConflict {
		t.Errorf("responding to an already-resolved request should be 409, got %d (body=%s)", rr.Code, rr.Body.String())
	}

	orphan := seedHILInboxRow(t, core, "trent", "req-no-pending")
	rr = postRespond(t, core, orphan, "trent", `{"status":"answered"}`)
	if rr.Code != http.StatusConflict {
		t.Errorf("responding with no live pending request should be 409, got %d", rr.Code)
	}
}

func TestStampPromptStates_DegradeWhenPendingNil(t *testing.T) {
	core := &NotifyCore{}
	data := []NotificationDTO{{ID: "n1", RequestID: "req-x"}}
	core.stampPromptStates(t.Context(), "trent", data)
	if data[0].PromptState != "" {
		t.Errorf("promptState = %q, want empty when the pending store is nil", data[0].PromptState)
	}
}

func TestStampPromptStates_LookupErrorLeavesUnset(t *testing.T) {
	core, _, _ := startRouterCore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	data := []NotificationDTO{{ID: "n1", RequestID: "req-x"}}
	core.stampPromptStates(ctx, "trent", data)
	if data[0].PromptState != "" {
		t.Errorf("promptState = %q, want empty when the liveness lookup errors", data[0].PromptState)
	}
}

func TestInboxList_StampsPromptState(t *testing.T) {
	core, _, ns := startRouterCore(t)

	const pendingReq = "req-live-pending"
	if err := core.pending.Insert(t.Context(), store.PendingRequest{
		RequestID: pendingReq, RequesterAddr: a2a.NotifyResponseTopic(ns, "agent-1", pendingReq), RecipientUserID: "trent",
	}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	seedHILInboxRow(t, core, "trent", pendingReq)

	const resolvedReq = "req-live-resolved"
	if err := core.pending.Insert(t.Context(), store.PendingRequest{
		RequestID: resolvedReq, RequesterAddr: a2a.NotifyResponseTopic(ns, "agent-1", resolvedReq), RecipientUserID: "trent",
	}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	if _, claimed, err := core.pending.Claim(t.Context(), resolvedReq, "answered"); err != nil || !claimed {
		t.Fatalf("claim resolvedReq: claimed=%v err=%v", claimed, err)
	}
	seedHILInboxRow(t, core, "trent", resolvedReq)

	const orphanReq = "req-live-orphan"
	seedHILInboxRow(t, core, "trent", orphanReq)

	if _, err := core.inbox.Add(t.Context(), "trent", "FYI", "done", map[string]any{"task_id": "t1"}, "notify_user", ""); err != nil {
		t.Fatalf("seed plain: %v", err)
	}

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox?userId=trent", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d", rr.Code)
	}
	var page PaginatedResponse[NotificationDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := make(map[string]string, len(page.Data))
	for _, d := range page.Data {
		got[d.RequestID] = d.PromptState
	}
	if got[pendingReq] != "pending" {
		t.Errorf("live pending HIL row promptState = %q, want pending", got[pendingReq])
	}
	if got[resolvedReq] != "resolved" {
		t.Errorf("terminal HIL row promptState = %q, want resolved", got[resolvedReq])
	}
	if got[orphanReq] != "resolved" {
		t.Errorf("orphan (no pending row) HIL row promptState = %q, want resolved", got[orphanReq])
	}
	if ps, ok := got[""]; ok && ps != "" {
		t.Errorf("non-HIL row must not carry promptState, got %q", ps)
	}
}

func TestInboxList_ExposesHILFields(t *testing.T) {
	core, _, _ := startRouterCore(t)
	seedHILInboxRow(t, core, "trent", "req-list-1")
	if _, err := core.inbox.Add(t.Context(), "trent", "FYI", "done", map[string]any{"task_id": "t1"}, "notify_user", ""); err != nil {
		t.Fatalf("seed plain: %v", err)
	}

	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox?userId=trent", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d", rr.Code)
	}
	var page PaginatedResponse[NotificationDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var hil, plain *NotificationDTO
	for i := range page.Data {
		switch page.Data[i].RequestID {
		case "req-list-1":
			hil = &page.Data[i]
		case "":
			plain = &page.Data[i]
		}
	}
	if hil == nil || len(hil.Surface) == 0 {
		t.Fatalf("HIL row must expose requestId + surface, got %+v", hil)
	}
	if plain == nil || plain.Surface != nil {
		t.Errorf("fire-and-forget row must not carry HIL fields, got %+v", plain)
	}
}
