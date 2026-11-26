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
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

func getMatrix(t *testing.T, core *NotifyCore) NotificationMatrixDTO {
	t.Helper()
	rr := do(core, http.MethodGet, "/api/v1/notify/preferences/matrix", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET matrix = %d; body=%s", rr.Code, rr.Body.String())
	}
	var m NotificationMatrixDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode matrix: %v", err)
	}
	return m
}

func putCells(core *NotifyCore, cells ...string) (int, string) {
	rr := do(core, http.MethodPut, "/api/v1/notify/preferences/matrix", `{"cells":[`+strings.Join(cells, ",")+`]}`)
	return rr.Code, rr.Body.String()
}

func cell(category, channelType string, enabled bool) string {
	return fmt.Sprintf(`{"category":%q,"channelType":%q,"destinationKey":"","enabled":%t}`, category, channelType, enabled)
}

func cellState(t *testing.T, m NotificationMatrixDTO, category, channelType string) MatrixCellDTO {
	t.Helper()
	for _, c := range m.Cells {
		if c.Category == category && c.ChannelType == channelType && c.DestinationKey == "" {
			return c
		}
	}
	t.Fatalf("no cell %s/%s in %+v", category, channelType, m.Cells)
	return MatrixCellDTO{}
}

func deliverRecord(t *testing.T, core *NotifyCore, category, channelType string) DeliveryRecord {
	t.Helper()
	recs := core.dispatcher.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "r-" + category + "-" + channelType, RecipientUserID: devUser, Mode: a2a.NotifyModeNotify,
		Category: category, Title: "t", Message: "m",
	})
	for _, r := range recs {
		if r.ChannelType == channelType {
			return r
		}
	}
	t.Fatalf("no %s record for %s in %+v", channelType, category, recs)
	return DeliveryRecord{}
}

func TestHTTP_Matrix_DefaultsToEverythingOnWithApprovalsLockedInTheInbox(t *testing.T) {
	core := newTestCore(t, true, nil)
	confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)

	m := getMatrix(t, core)
	if len(m.Categories) != len(Categories()) || len(m.Channels) != 2 ||
		m.Channels[0].ChannelType != inboxChannelType || m.Channels[1].ChannelType != "teams" {
		t.Fatalf("matrix shape = %+v", m)
	}
	if len(m.Cells) != len(m.Categories)*len(m.Channels) {
		t.Fatalf("cells = %d, want one per category and channel", len(m.Cells))
	}
	for _, c := range m.Cells {
		wantLocked := c.Category == CategoryHIL && c.ChannelType == inboxChannelType
		if !c.Enabled || c.Locked != wantLocked {
			t.Errorf("cell %+v, want enabled and locked=%v", c, wantLocked)
		}
	}
}

func TestHTTP_Matrix_CellsGateTheirOwnCategoryAndChannel(t *testing.T) {
	core := newTestCore(t, true, nil)
	confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)

	code, body := putCells(core, cell(CategoryScheduledTask, "teams", false), cell(CategoryAgentMessage, inboxChannelType, false))
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	var saved NotificationMatrixDTO
	if err := json.Unmarshal([]byte(body), &saved); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if cellState(t, saved, CategoryScheduledTask, "teams").Enabled || cellState(t, saved, CategoryAgentMessage, inboxChannelType).Enabled {
		t.Fatalf("the PUT response is not the updated matrix: %+v", saved.Cells)
	}
	m := getMatrix(t, core)
	if cellState(t, m, CategoryScheduledTask, "teams").Enabled || cellState(t, m, CategoryAgentMessage, inboxChannelType).Enabled {
		t.Fatalf("cells not saved: %+v", m.Cells)
	}
	if !cellState(t, m, CategoryScheduledTask, inboxChannelType).Enabled || !cellState(t, m, CategoryAgentMessage, "teams").Enabled {
		t.Fatalf("neighbouring cells changed: %+v", m.Cells)
	}

	if r := deliverRecord(t, core, CategoryScheduledTask, "teams"); r.Status != "skipped" {
		t.Errorf("scheduled task on teams = %+v, want skipped", r)
	}
	if r := deliverRecord(t, core, CategoryScheduledTask, inboxChannelType); r.Status != "sent" {
		t.Errorf("scheduled task in the inbox = %+v, want sent", r)
	}
	if r := deliverRecord(t, core, CategoryAgentMessage, inboxChannelType); r.Status != "skipped" {
		t.Errorf("agent message in the inbox = %+v, want skipped", r)
	}
	if r := deliverRecord(t, core, CategoryAgentMessage, "teams"); r.Status != "sent" {
		t.Errorf("agent message on teams = %+v, want sent", r)
	}
	inbox, err := core.inbox.ListByUser(t.Context(), devUser, store.InboxFilter{}, 50, 0)
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	if len(inbox) != 2 {
		t.Errorf("inbox rows = %d, want only the two scheduled-task notifications", len(inbox))
	}
}

func TestHTTP_Matrix_RejectsWhatTheUserCannotSet(t *testing.T) {
	core := newTestCore(t, true, nil)
	code, body := putCells(core,
		cell(CategoryHIL, inboxChannelType, false),
		cell("nope", inboxChannelType, false),
		cell(CategoryScheduledTask, "slack", false),
		`{"category":"chat_share","channelType":"web","destinationKey":""}`,
		cell(CategoryAgentMessage, inboxChannelType, false),
	)
	if code != http.StatusBadRequest {
		t.Fatalf("PUT = %d %s, want 400", code, body)
	}
	var apiErr struct{ Message string }
	if err := json.Unmarshal([]byte(body), &apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	for _, want := range []string{"cells[0]: Tool approvals and agent input requests always reach the in-app inbox", "cells[1]: unknown notification category", `cells[2]: you have no "slack" channel`, "cells[3]: enabled is required"} {
		if !strings.Contains(apiErr.Message, want) {
			t.Errorf("error %q does not mention %q", apiErr.Message, want)
		}
	}
	if !cellState(t, getMatrix(t, core), CategoryAgentMessage, inboxChannelType).Enabled {
		t.Error("a rejected save must not change any cell")
	}
}

func TestHTTP_Matrix_SavingOneCellKeepsOlderRowAndColumnOptOuts(t *testing.T) {
	core := newTestCore(t, true, nil)
	confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences/categories", `{"category":"scheduled_task","enabled":false}`); rr.Code != http.StatusOK {
		t.Fatalf("mute category = %d", rr.Code)
	}
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences", `{"channelType":"teams","destinationKey":"","enabled":false}`); rr.Code != http.StatusOK {
		t.Fatalf("disable channel = %d %s", rr.Code, rr.Body.String())
	}

	m := getMatrix(t, core)
	for _, c := range [][2]string{{CategoryScheduledTask, inboxChannelType}, {CategoryScheduledTask, "teams"}, {CategoryAgentMessage, "teams"}} {
		if cellState(t, m, c[0], c[1]).Enabled {
			t.Fatalf("cell %v should show the older opt-out as off", c)
		}
	}

	if code, body := putCells(core, cell(CategoryScheduledTask, inboxChannelType, true), cell(CategoryAgentMessage, "teams", true)); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	m = getMatrix(t, core)
	if !cellState(t, m, CategoryScheduledTask, inboxChannelType).Enabled || !cellState(t, m, CategoryAgentMessage, "teams").Enabled {
		t.Fatalf("requested cells not on: %+v", m.Cells)
	}
	for _, c := range [][2]string{{CategoryScheduledTask, "teams"}, {CategoryChatShare, "teams"}, {CategoryHIL, "teams"}} {
		if cellState(t, m, c[0], c[1]).Enabled {
			t.Errorf("cell %v was turned on by clearing an older row or column opt-out", c)
		}
	}
	if r := deliverRecord(t, core, CategoryScheduledTask, "teams"); r.Status != "skipped" {
		t.Errorf("scheduled task on teams = %+v, want still skipped", r)
	}
	if r := deliverRecord(t, core, CategoryScheduledTask, inboxChannelType); r.Status != "sent" {
		t.Errorf("scheduled task in the inbox = %+v, want sent", r)
	}
}

func TestHTTP_Matrix_SavingOneCellOfAMutedRowKeepsTheRestOfTheRowOff(t *testing.T) {
	core := newTestCore(t, true, nil)
	confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences/categories", `{"category":"chat_share","enabled":false}`); rr.Code != http.StatusOK {
		t.Fatalf("mute category = %d", rr.Code)
	}

	if code, body := putCells(core, cell(CategoryChatShare, inboxChannelType, true)); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	m := getMatrix(t, core)
	if !cellState(t, m, CategoryChatShare, inboxChannelType).Enabled {
		t.Fatal("the requested cell is not on")
	}
	if cellState(t, m, CategoryChatShare, "teams").Enabled {
		t.Error("clearing the row mute turned on a cell the save did not name")
	}
	if !cellState(t, m, CategoryScheduledTask, "teams").Enabled {
		t.Error("another row changed")
	}
}

type failingCells struct{}

func (failingCells) DisabledCells(context.Context, string) (map[string]bool, error) {
	return nil, errors.New("db down")
}

func TestDispatcher_MatrixLookupFailureKeepsTheInbox(t *testing.T) {
	pub := &fakeProactivePub{}
	d := proactiveDispatcher(pub, &fakeLearned{addrs: map[string]string{"slack": `{"channel_id":"D1"}`}}, nil)
	fi := &fakeInbox{}
	d.inbox = fi
	d.SetCellPreferences(failingCells{})
	d.logger = slog.New(slog.DiscardHandler)

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Category: CategoryScheduledTask, Title: "t"})
	if len(fi.calls) != 1 {
		t.Fatalf("inbox writes = %d, want 1", len(fi.calls))
	}
	if len(pub.calls) != 0 {
		t.Fatalf("delivers = %+v, want none", pub.calls)
	}
	for _, r := range recs {
		if r.ChannelType == "slack" && r.Status != "skipped" {
			t.Errorf("slack = %+v, want skipped", r)
		}
	}
}

func TestHTTP_Matrix_SavingKeepsAMuteOnAChannelThatIsNotAColumnYet(t *testing.T) {
	core := newTestCore(t, true, nil)
	if err := core.channels.Upsert(t.Context(), store.UserChannel{UserID: devUser, ChannelType: "slack", Address: `{"channel_id":"D1"}`}); err != nil {
		t.Fatalf("learn channel: %v", err)
	}
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences/categories", `{"category":"chat_share","enabled":false}`); rr.Code != http.StatusOK {
		t.Fatalf("mute category = %d", rr.Code)
	}
	if code, body := putCells(core, cell(CategoryChatShare, inboxChannelType, true)); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}

	confirmChannel(t, core, devUser, "slack", `{"channel_id":"D1"}`)
	m := getMatrix(t, core)
	if cellState(t, m, CategoryChatShare, "slack").Enabled {
		t.Error("confirming the channel turned on a category the user had muted")
	}
	if !cellState(t, m, CategoryScheduledTask, "slack").Enabled {
		t.Error("a category that was never muted is off on the new channel")
	}
}

type failingDirectory struct{}

func (failingDirectory) ChannelsFor(context.Context, string) ([]Channel, error) {
	return nil, errors.New("directory down")
}

func TestHTTP_Matrix_DirectoryFailureIsAnErrorNotADegradedMatrix(t *testing.T) {
	core := newTestCore(t, true, nil)
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences/categories", `{"category":"chat_share","enabled":false}`); rr.Code != http.StatusOK {
		t.Fatalf("mute category = %d", rr.Code)
	}
	core.dir = failingDirectory{}

	if rr := do(core, http.MethodGet, "/api/v1/notify/preferences/matrix", ""); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET = %d, want 500", rr.Code)
	}
	if code, body := putCells(core, cell(CategoryChatShare, inboxChannelType, true)); code != http.StatusInternalServerError {
		t.Errorf("PUT = %d %s, want 500", code, body)
	}

	core.dir = newConfigDirectory(nil)
	if cellState(t, getMatrix(t, core), CategoryChatShare, inboxChannelType).Enabled {
		t.Error("the failed save cleared the older mute")
	}
}

func serveAs(h http.HandlerFunc, method, target, body string, id *auth.Subject) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func TestHTTP_Matrix_CrossUserScopes(t *testing.T) {
	core := newTestCore(t, true, nil)
	const target = "/api/v1/notify/preferences/matrix?userId=bob"
	as := func(scopes ...string) *auth.Subject {
		return auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: scopes})
	}

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
		scopes  []string
		want    int
	}{
		{"read without scope", core.handleMatrixGet, http.MethodGet, "", nil, http.StatusForbidden},
		{"read with read_others", core.handleMatrixGet, http.MethodGet, "", []string{"notify:_:read_others"}, http.StatusOK},
		{"write with only read_others", core.handleMatrixUpdate, http.MethodPut, `{"cells":[]}`, []string{"notify:_:read_others"}, http.StatusForbidden},
		{"write with send_others", core.handleMatrixUpdate, http.MethodPut, `{"cells":[]}`, []string{"notify:_:send_others"}, http.StatusOK},
	} {
		if rr := serveAs(tc.handler, tc.method, target, tc.body, as(tc.scopes...)); rr.Code != tc.want {
			t.Errorf("%s = %d %s, want %d", tc.name, rr.Code, rr.Body.String(), tc.want)
		}
	}
}

func TestHTTP_Matrix_UnavailableWithoutDatabase(t *testing.T) {
	core := newTestCore(t, false, nil)
	if rr := do(core, http.MethodGet, "/api/v1/notify/preferences/matrix", ""); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("GET = %d, want 503", rr.Code)
	}
	if code, body := putCells(core, cell(CategoryChatShare, inboxChannelType, false)); code != http.StatusServiceUnavailable {
		t.Errorf("PUT = %d %s, want 503", code, body)
	}
}

func TestHTTP_Matrix_RejectsUnknownFieldsAndDuplicates(t *testing.T) {
	core := newTestCore(t, true, nil)

	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences/matrix", `{"cells":[],"extra":1}`); rr.Code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", rr.Code)
	}

	code, body := putCells(core, cell(CategoryAgentMessage, inboxChannelType, false), cell(CategoryAgentMessage, inboxChannelType, true))
	if code != http.StatusBadRequest || !strings.Contains(body, "already set by an earlier cell") {
		t.Errorf("duplicate cell = %d %s, want 400 naming the duplicate", code, body)
	}
	if !cellState(t, getMatrix(t, core), CategoryAgentMessage, inboxChannelType).Enabled {
		t.Error("a rejected save changed a cell")
	}
}

type fakeCells map[string]bool

func (f fakeCells) DisabledCells(context.Context, string) (map[string]bool, error) { return f, nil }

func TestDispatcher_StoredInboxOptOutOfApprovalsStillReachesTheInbox(t *testing.T) {
	fi := &fakeInbox{}
	d := NewDispatcher(newConfigDirectory(nil), fi, slog.New(slog.DiscardHandler))
	d.SetCellPreferences(fakeCells{store.CellKey(CategoryHIL, inboxChannelType, ""): true})

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Category: CategoryHIL, Title: "t"})
	if len(fi.calls) != 1 || len(recs) != 1 || recs[0].Status != "sent" {
		t.Fatalf("inbox writes = %d, records = %+v, want one sent inbox record", len(fi.calls), recs)
	}
}

func TestHTTP_Matrix_GatesDirectoryDestinationsIndependently(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer hook.Close()
	core := newTestCore(t, true, nil)
	dir := newConfigDirectory(map[string][]Channel{devUser: {
		{Type: "webhook", Config: map[string]any{"url": hook.URL + "/a"}},
		{Type: "webhook", Config: map[string]any{"url": hook.URL + "/b"}},
		{Type: "connector", Config: map[string]any{"tool_name": "email_send"}},
	}})
	core.dir = dir
	core.dispatcher.dir = dir
	core.dispatcher.httpClient = hook.Client()
	core.dispatcher.skipURLValidation = true

	var hookA, connector MatrixChannelDTO
	for _, ch := range getMatrix(t, core).Channels {
		switch {
		case ch.ChannelType == "webhook" && hookA.ChannelType == "":
			hookA = ch
		case ch.ChannelType == "connector":
			connector = ch
		}
	}
	off := func(ch MatrixChannelDTO) string {
		return fmt.Sprintf(`{"category":%q,"channelType":%q,"destinationKey":%q,"enabled":false}`, CategoryScheduledTask, ch.ChannelType, ch.DestinationKey)
	}
	if code, body := putCells(core, off(hookA), off(connector)); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}

	const disabled = "channel disabled by user preference"
	outOfBand := func(category string) []DeliveryRecord {
		var out []DeliveryRecord
		for _, r := range core.dispatcher.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: devUser, Mode: a2a.NotifyModeNotify, Category: category, Title: "t"}) {
			if r.ChannelType != inboxChannelType {
				out = append(out, r)
			}
		}
		return out
	}

	recs := outOfBand(CategoryScheduledTask)
	if len(recs) != 3 || recs[0].Error != disabled || recs[1].Status != "sent" || recs[2].Error != disabled {
		t.Errorf("scheduled task out-of-band = %+v, want the first webhook and the connector disabled and the second webhook sent", recs)
	}
	for _, r := range outOfBand(CategoryAgentMessage) {
		if r.Error == disabled {
			t.Errorf("agent message on %s = %+v, want it untouched by the scheduled-task opt-out", r.ChannelType, r)
		}
	}
}

func mutedChatShareAndOptedOutTeams(t *testing.T) *NotifyCore {
	t.Helper()
	core := newTestCore(t, true, nil)
	confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences/categories", `{"category":"chat_share","enabled":false}`); rr.Code != http.StatusOK {
		t.Fatalf("mute category = %d", rr.Code)
	}
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences", `{"channelType":"teams","destinationKey":"","enabled":false}`); rr.Code != http.StatusOK {
		t.Fatalf("disable channel = %d %s", rr.Code, rr.Body.String())
	}
	return core
}

func TestHTTP_Matrix_SavingARowDoesNotClearAnUntouchedColumnOptOut(t *testing.T) {
	core := mutedChatShareAndOptedOutTeams(t)

	if code, body := putCells(core, cell(CategoryChatShare, inboxChannelType, true)); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	m := getMatrix(t, core)
	if !cellState(t, m, CategoryChatShare, inboxChannelType).Enabled {
		t.Fatal("the requested cell is not on")
	}
	for _, category := range []string{CategoryScheduledTask, CategoryAgentMessage, CategoryChatShare, CategoryHIL} {
		if cellState(t, m, category, "teams").Enabled {
			t.Errorf("%s on teams was turned on by a save that did not name the teams column", category)
		}
	}
	if r := deliverRecord(t, core, CategoryScheduledTask, "teams"); r.Status != "skipped" {
		t.Errorf("scheduled task on teams = %+v, want still skipped", r)
	}
}

func TestHTTP_Matrix_SavingAColumnDoesNotClearAnUntouchedRowMute(t *testing.T) {
	core := mutedChatShareAndOptedOutTeams(t)

	if code, body := putCells(core, cell(CategoryScheduledTask, "teams", true)); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	m := getMatrix(t, core)
	if !cellState(t, m, CategoryScheduledTask, "teams").Enabled {
		t.Fatal("the requested cell is not on")
	}
	for _, channel := range []string{inboxChannelType, "teams"} {
		if cellState(t, m, CategoryChatShare, channel).Enabled {
			t.Errorf("chat_share on %s was turned on by a save that did not name the chat_share row", channel)
		}
	}
	recs := core.dispatcher.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: devUser, Mode: a2a.NotifyModeNotify, Category: CategoryChatShare, Title: "t", Message: "m"})
	if len(recs) != 0 {
		t.Errorf("chat_share deliveries = %+v, want none for a muted category", recs)
	}
}

func TestHTTP_Matrix_SavingARowKeepsAnUnconfirmedChannelsColumnOptOut(t *testing.T) {
	core := newTestCore(t, true, nil)
	if err := core.channels.Upsert(t.Context(), store.UserChannel{UserID: devUser, ChannelType: "slack", Address: `{"channel_id":"D1"}`}); err != nil {
		t.Fatalf("learn channel: %v", err)
	}
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences/categories", `{"category":"chat_share","enabled":false}`); rr.Code != http.StatusOK {
		t.Fatalf("mute category = %d", rr.Code)
	}
	if rr := do(core, http.MethodPut, "/api/v1/notify/preferences", `{"channelType":"slack","destinationKey":"","enabled":false}`); rr.Code != http.StatusOK {
		t.Fatalf("disable channel = %d %s", rr.Code, rr.Body.String())
	}
	if code, body := putCells(core, cell(CategoryChatShare, inboxChannelType, true)); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}

	confirmChannel(t, core, devUser, "slack", `{"channel_id":"D1"}`)
	m := getMatrix(t, core)
	for _, category := range []string{CategoryScheduledTask, CategoryAgentMessage, CategoryChatShare, CategoryHIL} {
		if cellState(t, m, category, "slack").Enabled {
			t.Errorf("%s on slack was enabled by confirming a channel the user had opted out of", category)
		}
	}
}

func TestHTTP_Matrix_ConcurrentSavesKeepEveryRequestedCell(t *testing.T) {
	for i := range 20 {
		core := newTestCore(t, true, nil)
		confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)
		if rr := do(core, http.MethodPut, "/api/v1/notify/preferences", `{"channelType":"teams","destinationKey":"","enabled":false}`); rr.Code != http.StatusOK {
			t.Fatalf("disable channel = %d %s", rr.Code, rr.Body.String())
		}

		var wg sync.WaitGroup
		for _, category := range []string{CategoryScheduledTask, CategoryAgentMessage} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if code, body := putCells(core, cell(category, "teams", true)); code != http.StatusOK {
					t.Errorf("PUT %s = %d %s", category, code, body)
				}
			}()
		}
		wg.Wait()

		m := getMatrix(t, core)
		for _, category := range []string{CategoryScheduledTask, CategoryAgentMessage} {
			if !cellState(t, m, category, "teams").Enabled {
				t.Fatalf("run %d: %s on teams was lost by the concurrent save", i, category)
			}
		}
	}
}

func TestHTTP_Preferences_ListKeepsConfirmedChannelsWhenTheDirectoryFails(t *testing.T) {
	core := newTestCore(t, true, nil)
	confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)
	core.dir = failingDirectory{}

	rr := do(core, http.MethodGet, "/api/v1/notify/preferences", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rr.Code, rr.Body.String())
	}
	var list PaginatedResponse[PreferenceDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0].ChannelType != "teams" {
		t.Fatalf("preferences = %+v, want the confirmed teams channel only", list.Data)
	}
}

func TestHTTP_Matrix_UnconfirmedChannelHasNoColumnAndRefusesCells(t *testing.T) {
	core := newTestCore(t, true, nil)
	if err := core.channels.Upsert(t.Context(), store.UserChannel{UserID: devUser, ChannelType: "slack", Address: `{"channel_id":"D1"}`}); err != nil {
		t.Fatalf("learn channel: %v", err)
	}
	if m := getMatrix(t, core); len(m.Channels) != 1 {
		t.Fatalf("channels = %+v, want only the in-app column", m.Channels)
	}
	if code, body := putCells(core, cell(CategoryScheduledTask, "slack", false)); code != http.StatusBadRequest {
		t.Errorf("PUT = %d %s, want 400", code, body)
	}
}

func TestHTTP_Matrix_CategoriesCarryTheirGroupAndDescription(t *testing.T) {
	core := newTestCore(t, true, nil)

	m := getMatrix(t, core)

	want := Categories()
	if len(m.Categories) != len(want) {
		t.Fatalf("categories = %d, want %d", len(m.Categories), len(want))
	}
	for i, c := range want {
		got := m.Categories[i]
		if got.Category != c.Key || got.Label != c.Label || got.Group != c.Group || got.Description != c.Description {
			t.Errorf("category %d = %+v, want %+v", i, got, c)
		}
	}
	hil := m.Categories[slices.IndexFunc(m.Categories, func(c MatrixCategoryDTO) bool { return c.Category == CategoryHIL })]
	if hil.Group != "Requests and Sharing" || hil.Description == "" {
		t.Errorf("hil category = %+v, want the Requests and Sharing group and a description", hil)
	}
}
