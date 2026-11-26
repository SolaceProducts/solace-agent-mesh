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
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	v2Alice = "alice@example.com"
	v2Bob   = "bob@example.com"
)

type v2Session struct {
	t    *testing.T
	a    *HTTPSSEAdapter
	core *entrypoint.GatewayCore
	sess *store.SessionRepo
	chat *store.ChatTaskRepo
	sse  *store.SSEEventRepo
}

func newV2Session(t *testing.T) *v2Session {
	t.Helper()
	a, core, sess, chat, sse := setupTestAdapterWithDB(t)
	return &v2Session{t: t, a: a, core: core, sess: sess, chat: chat, sse: sse}
}

func (f *v2Session) call(method, path, caller, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := withIdentity(httptest.NewRequest(method, chatv2.Prefix+path, reader), caller)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.a.Mux().ServeHTTP(rec, req)
	return rec
}

func (f *v2Session) create(caller string) chatv2.Session {
	f.t.Helper()
	rec := f.call(http.MethodPost, "/sessions", caller, `{}`)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("create session = %d %s, want 201", rec.Code, rec.Body)
	}
	return decodeData[chatv2.Session](f.t, rec)
}

func decodeData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return body.Data
}

func (f *v2Session) save(caller, sessionID, taskID, body string) *httptest.ResponseRecorder {
	return f.call(http.MethodPut, "/sessions/"+sessionID+"/chatTasks/"+taskID, caller, body)
}

const minimalTurn = `{"messageBubbles":[{"type":"user","text":"hello"},{"type":"agent","text":"hi there"}],"taskMetadata":{"agentName":"Support"}}`

func TestChatV2CreateSessionAssignsTheIdAndOwner(t *testing.T) {
	f := newV2Session(t)

	session := f.create(v2Alice)

	if session.ID == "" || session.Type != "session" || session.CreatedBy != v2Alice {
		t.Fatalf("session = %+v, want a server id, type session and alice as owner", session)
	}
	if session.CreatedTime == 0 {
		t.Errorf("createdTime is unset")
	}
	row, err := f.sess.GetByID(context.Background(), session.ID)
	if err != nil || row == nil || row.UserID != v2Alice {
		t.Fatalf("stored session = %+v, %v, want alice's", row, err)
	}
}

func TestChatV2CreateSessionRefusesAClientId(t *testing.T) {
	f := newV2Session(t)

	rec := f.call(http.MethodPost, "/sessions", v2Alice, `{"id":"`+idgen.New()+`"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d %s, want 400 for a body field the route does not take", rec.Code, rec.Body)
	}
}

func TestChatV2CreateSessionOnlyAcceptsTheBuilderSource(t *testing.T) {
	f := newV2Session(t)

	if rec := f.call(http.MethodPost, "/sessions", v2Alice, `{"source":"scheduler"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("source scheduler = %d, want 422", rec.Code)
	}
	rec := f.call(http.MethodPost, "/sessions", v2Alice, `{"source":"builder_test"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("source builder_test = %d %s, want 201", rec.Code, rec.Body)
	}
	if got := decodeData[chatv2.Session](t, rec); got.Source == nil || *got.Source != "builder_test" {
		t.Errorf("source = %v, want builder_test", got.Source)
	}
}

func TestChatV2SessionRoutesAnswer404ToAnotherUser(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	taskID := idgen.New()
	if rec := f.save(v2Alice, session.ID, taskID, minimalTurn); rec.Code != http.StatusOK {
		t.Fatalf("alice saves her own turn = %d %s", rec.Code, rec.Body)
	}
	id := session.ID
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/sessions/" + id, ""},
		{http.MethodPatch, "/sessions/" + id, `{"name":"mine now"}`},
		{http.MethodDelete, "/sessions/" + id, ""},
		{http.MethodPut, "/sessions/" + id + "/project", `{"projectId":null}`},
		{http.MethodPut, "/sessions/" + id + "/viewed", ""},
		{http.MethodPut, "/sessions/" + id + "/title", `{}`},
		{http.MethodGet, "/sessions/" + id + "/messages", ""},
		{http.MethodGet, "/sessions/" + id + "/events/unconsumed", ""},
		{http.MethodGet, "/sessions/" + id + "/contextUsage", ""},
		{http.MethodPut, "/sessions/" + id + "/compaction", `{}`},
		{http.MethodPut, "/sessions/" + id + "/chatTasks/" + idgen.New(), minimalTurn},
		{http.MethodGet, "/sessions/" + id + "/chatTasks", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := f.call(tc.method, tc.path, v2Bob, tc.body)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("bob got %d %s, want 404", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), "hello") || strings.Contains(rec.Body.String(), v2Alice) {
				t.Errorf("the 404 leaks alice's data: %s", rec.Body)
			}
		})
	}
	row, _ := f.sess.GetByID(context.Background(), id)
	if row == nil {
		t.Fatalf("bob's requests deleted alice's session")
	}
	if row.Name != nil && *row.Name == "mine now" || row.LastViewedAt != nil || row.ProjectID != nil {
		t.Errorf("bob's refused requests changed alice's session: %+v", row)
	}
	tasks, _ := f.chat.ListBySession(context.Background(), id)
	if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Errorf("alice's history holds %d tasks after bob's requests, want her one", len(tasks))
	}
}

func TestChatV2SessionOwnerIsServed(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	f.save(v2Alice, session.ID, idgen.New(), minimalTurn)
	id := session.ID
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/sessions/" + id, ""},
		{http.MethodPatch, "/sessions/" + id, `{"name":"Quarterly"}`},
		{http.MethodPut, "/sessions/" + id + "/project", `{"projectId":null}`},
		{http.MethodPut, "/sessions/" + id + "/viewed", ""},
		{http.MethodGet, "/sessions/" + id + "/messages", ""},
		{http.MethodGet, "/sessions/" + id + "/events/unconsumed", ""},
		{http.MethodGet, "/sessions/" + id + "/contextUsage", ""},
		{http.MethodGet, "/sessions/" + id + "/chatTasks", ""},
		{http.MethodGet, "/sessions", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if rec := f.call(tc.method, tc.path, v2Alice, tc.body); rec.Code != http.StatusOK {
				t.Fatalf("owner got %d %s, want 200", rec.Code, rec.Body)
			}
		})
	}
}

func TestChatV2SaveWithOnlyTypeAndTextIsReadBackEverywhere(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	taskID := idgen.New()

	rec := f.save(v2Alice, session.ID, taskID, `{"messageBubbles":[{"type":"user","text":"hello"},{"type":"agent","text":"hi there"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s, want 200", rec.Code, rec.Body)
	}

	messages := f.call(http.MethodGet, "/sessions/"+session.ID+"/messages", v2Alice, "")
	var got struct {
		Data []chatv2.SessionMessage `json:"data"`
	}
	if err := json.Unmarshal(messages.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 2 || got.Data[0].Text != "hello" || got.Data[0].SenderType != "user" || got.Data[1].Text != "hi there" || got.Data[1].SenderType != "agent" {
		t.Errorf("messages = %+v, want hello from the user and hi there from the agent", got.Data)
	}
	tasks := f.call(http.MethodGet, "/sessions/"+session.ID+"/chatTasks", v2Alice, "")
	var saved struct {
		Data []chatv2.SessionChatTask `json:"data"`
	}
	if err := json.Unmarshal(tasks.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Data) != 1 || saved.Data[0].ID != taskID || len(saved.Data[0].MessageBubbles) != 2 {
		t.Errorf("chat tasks = %+v, want the one saved turn", saved.Data)
	}
}

func TestChatV2SaveKeepsExtensionKeysUnchanged(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	body := `{"messageBubbles":[{"type":"agent","text":"done","renderHint":{"collapsed":true,"order":[3,1,2]},"isStreaming":false}],"taskMetadata":{"agentName":"Support","status":"completed","uiState":{"scroll":120}}}`

	if rec := f.save(v2Alice, session.ID, idgen.New(), body); rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body)
	}

	tasks := f.call(http.MethodGet, "/sessions/"+session.ID+"/chatTasks", v2Alice, "")
	var raw struct {
		Data []struct {
			MessageBubbles []map[string]any `json:"messageBubbles"`
			TaskMetadata   map[string]any   `json:"taskMetadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tasks.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	bubble := raw.Data[0].MessageBubbles[0]
	hint, _ := bubble["renderHint"].(map[string]any)
	if hint["collapsed"] != true || bubble["isStreaming"] != false || bubble["type"] != "agent" || bubble["text"] != "done" {
		t.Errorf("bubble = %v, want its extension keys back as sent", bubble)
	}
	ui, _ := raw.Data[0].TaskMetadata["uiState"].(map[string]any)
	if ui["scroll"] != float64(120) || raw.Data[0].TaskMetadata["status"] != "completed" || raw.Data[0].TaskMetadata["agentName"] != "Support" {
		t.Errorf("taskMetadata = %v, want its extension keys back as sent", raw.Data[0].TaskMetadata)
	}
}

func TestChatV2SaveRefusesABubbleWithoutTypeOrText(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	cases := map[string]string{
		"no type":         `{"messageBubbles":[{"text":"hello"}]}`,
		"no text":         `{"messageBubbles":[{"type":"user"}]}`,
		"unknown type":    `{"messageBubbles":[{"type":"robot","text":"x"}]}`,
		"system type":     `{"messageBubbles":[{"type":"system","text":"x"}]}`,
		"no bubbles":      `{"messageBubbles":[]}`,
		"bubbles missing": `{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := f.save(v2Alice, session.ID, idgen.New(), body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d %s, want 422", rec.Code, rec.Body)
			}
		})
	}
}

func TestChatV2SaveAllowsAnEmptyText(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)

	rec := f.save(v2Alice, session.ID, idgen.New(), `{"messageBubbles":[{"type":"agent","text":""}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200: an empty text is still a text", rec.Code, rec.Body)
	}
}

func TestChatV2SaveCountsAVersionedArtifactRefAsAReferencedUserFile(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	taskID := idgen.New()
	body := `{"messageBubbles":[{"type":"agent","text":"here","parts":[{"kind":"artifact","file":{"uri":"artifact://` + session.ID + `/user:report.pdf?version=2"}}]}]}`

	if rec := f.save(v2Alice, session.ID, taskID, body); rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body)
	}

	tasks, err := f.chat.ListBySession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := entrypoint.UserScopedFilesReferencedBy(tasks, v2Alice)
	if err != nil {
		t.Fatal(err)
	}
	if !refs.Has("user:report.pdf", 2) {
		t.Errorf("referenced files = %v, want user:report.pdf at version 2", refs)
	}
}

func TestChatV2SaveRefusesATaskAnotherUserOwns(t *testing.T) {
	f := newV2Session(t)
	alice, bob := f.create(v2Alice), f.create(v2Bob)
	taskID := idgen.New()
	if rec := f.save(v2Alice, alice.ID, taskID, minimalTurn); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}

	rec := f.save(v2Bob, bob.ID, taskID, `{"messageBubbles":[{"type":"user","text":"mine"}]}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d %s, want 404", rec.Code, rec.Body)
	}
	kept, _ := f.chat.GetByID(context.Background(), taskID)
	if kept == nil || kept.UserID != v2Alice || strings.Contains(kept.MessageBubbles, "mine") {
		t.Errorf("alice's turn was changed: %+v", kept)
	}
}

func TestChatV2SaveRefusesAMalformedPathId(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)

	for _, path := range []string{"/sessions/not-a-uuid", "/sessions/not-a-uuid/messages", "/sessions/" + session.ID + "/chatTasks/not-a-uuid"} {
		method, body := http.MethodGet, ""
		if strings.Contains(path, "chatTasks/") {
			method, body = http.MethodPut, minimalTurn
		}
		if rec := f.call(method, path, v2Alice, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s = %d, want 400", method, path, rec.Code)
		}
	}
}

func TestChatV2ListPagesAlwaysAndWrapsTheList(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	for range 3 {
		f.save(v2Alice, session.ID, idgen.New(), minimalTurn)
	}

	rec := f.call(http.MethodGet, "/sessions/"+session.ID+"/chatTasks?pageSize=2", v2Alice, "")

	var page struct {
		Data []chatv2.SessionChatTask `json:"data"`
		Meta struct {
			Pagination struct {
				Count    int  `json:"count"`
				NextPage *int `json:"nextPage"`
			} `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 2 || page.Meta.Pagination.Count != 3 || page.Meta.Pagination.NextPage == nil || *page.Meta.Pagination.NextPage != 2 {
		t.Errorf("page = %+v, want 2 of 3 with nextPage 2", page)
	}
	unparamed := f.call(http.MethodGet, "/sessions/"+session.ID+"/messages", v2Alice, "")
	if !strings.Contains(unparamed.Body.String(), `"pagination"`) {
		t.Errorf("a list without paging parameters is not paged: %s", unparamed.Body)
	}
}

func TestChatV2ListSessionsShowsOnlyTheCallersNonEmptySessions(t *testing.T) {
	f := newV2Session(t)
	mine, empty, theirs := f.create(v2Alice), f.create(v2Alice), f.create(v2Bob)
	f.save(v2Alice, mine.ID, idgen.New(), minimalTurn)
	f.save(v2Bob, theirs.ID, idgen.New(), minimalTurn)

	rec := f.call(http.MethodGet, "/sessions", v2Alice, "")

	ids := map[string]bool{}
	for _, s := range decodeData[[]chatv2.Session](t, rec) {
		ids[s.ID] = true
	}
	if !ids[mine.ID] || ids[empty.ID] || ids[theirs.ID] {
		t.Errorf("listed %v, want only alice's non-empty session %s", ids, mine.ID)
	}
}

func TestChatV2ListSessionsRejectsABadFilter(t *testing.T) {
	f := newV2Session(t)

	cases := []struct {
		query      string
		wantStatus int
	}{
		{"?source=other", http.StatusUnprocessableEntity},
		{"?projectId=nope", http.StatusBadRequest},
		{"?pageSize=0", http.StatusUnprocessableEntity},
		{"?pagesize=5", http.StatusBadRequest},
		{"?includeAgentIds=x", http.StatusBadRequest},
	}
	for _, tc := range cases {
		rec := f.call(http.MethodGet, "/sessions"+tc.query, v2Alice, "")
		if rec.Code != tc.wantStatus {
			t.Errorf("%s = %d, want %d", tc.query, rec.Code, tc.wantStatus)
		}
	}
}

func TestChatV2SetProjectRejectsABadBody(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	cases := []struct {
		name, body string
		want       int
	}{
		{"malformed id", `{"projectId":"nope"}`, http.StatusUnprocessableEntity},
		{"omitted key", `{}`, http.StatusUnprocessableEntity},
		{"project the caller cannot read", `{"projectId":"` + idgen.New() + `"}`, http.StatusNotFound},
		{"null detaches", `{"projectId":null}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.call(http.MethodPut, "/sessions/"+session.ID+"/project", v2Alice, tc.body)
			if rec.Code != tc.want {
				t.Errorf("%s = %d %s, want %d", tc.body, rec.Code, rec.Body, tc.want)
			}
		})
	}
}

func TestChatV2SaveRejectsAMistypedBubbleKey(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	for _, bubble := range []string{
		`{"type":"user","text":"x","isError":"yes"}`,
		`{"type":"user","text":"x","parts":["report.pdf"]}`,
		`{"type":"user","text":"x","id":5}`,
	} {
		rec := f.save(v2Alice, session.ID, idgen.New(), `{"messageBubbles":[`+bubble+`]}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %s, want 422", bubble, rec.Code, rec.Body)
		}
	}
}

func TestChatV2ListChatTasksSkipsARowThatDoesNotDecode(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	f.save(v2Alice, session.ID, idgen.New(), minimalTurn)
	if _, err := f.chat.Upsert(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: session.ID, UserID: v2Alice, MessageBubbles: `{}`,
	}); err != nil {
		t.Fatalf("store a row v1 would accept: %v", err)
	}

	rec := f.call(http.MethodGet, "/sessions/"+session.ID+"/chatTasks", v2Alice, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", rec.Code, rec.Body)
	}
	if tasks := decodeData[[]chatv2.SessionChatTask](t, rec); len(tasks) != 1 {
		t.Errorf("listed %d tasks, want only the one that decodes", len(tasks))
	}
}

func TestChatV2ResaveReplacesTheTurnAndPendingStaysUnread(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	taskID := idgen.New()
	pending := `{"messageBubbles":[{"type":"user","text":"hello"}],"taskMetadata":{"status":"pending"}}`
	if rec := f.save(v2Alice, session.ID, taskID, pending); rec.Code != http.StatusOK {
		t.Fatalf("pending save = %d %s", rec.Code, rec.Body)
	}

	if rec := f.save(v2Alice, session.ID, taskID, minimalTurn); rec.Code != http.StatusOK {
		t.Fatalf("owner re-save = %d %s, want 200", rec.Code, rec.Body)
	}

	rec := f.call(http.MethodGet, "/sessions/"+session.ID+"/chatTasks", v2Alice, "")
	tasks := decodeData[[]chatv2.SessionChatTask](t, rec)
	if len(tasks) != 1 || len(tasks[0].MessageBubbles) != 2 {
		t.Errorf("after re-save got %+v, want one task with two bubbles", tasks)
	}
}

func TestChatV2ChatTaskFieldsAreAlwaysSentAndStoredCompact(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	taskID := idgen.New()
	f.save(v2Alice, session.ID, taskID, `{"messageBubbles":[{"type":"user","text":"hi"}],"taskMetadata":{}}`)

	rec := f.call(http.MethodGet, "/sessions/"+session.ID+"/chatTasks", v2Alice, "")

	for _, key := range []string{`"id":null`, `"isError":null`, `"parts":null`, `"agentName":null`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("response lacks %s: %s", key, rec.Body)
		}
	}
	stored, _ := f.chat.GetByID(context.Background(), taskID)
	if strings.Contains(stored.MessageBubbles, "null") {
		t.Errorf("stored bubbles carry nulls a v1 reader never saw: %s", stored.MessageBubbles)
	}
}

func TestChatV2CompactRefusesASessionWhoseAgentTheCallerMayNotUse(t *testing.T) {
	f := newV2Session(t)
	f.core.SetScopeResolver(scopeResolverFunc(func(string, []string) []string { return []string{"agent:other:invoke"} }))
	rec := f.call(http.MethodPost, "/sessions", v2Alice, `{"agentId":"Support"}`)
	session := decodeData[chatv2.Session](t, rec)

	got := f.call(http.MethodPut, "/sessions/"+session.ID+"/compaction", v2Alice, `{}`)

	if got.Code != http.StatusForbidden {
		t.Errorf("compaction = %d %s, want 403", got.Code, got.Body)
	}
}

func TestChatV2SessionCarriesANestedProject(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)

	rec := f.call(http.MethodGet, "/sessions/"+session.ID, v2Alice, "")

	if !strings.Contains(rec.Body.String(), `"project":null`) || strings.Contains(rec.Body.String(), `"projectId"`) {
		t.Errorf("session = %s, want project null and no flat projectId", rec.Body)
	}
}

func TestChatV2ListSessionsNextPageEndsOnTheLastPage(t *testing.T) {
	f := newV2Session(t)
	for range 3 {
		f.save(v2Alice, f.create(v2Alice).ID, idgen.New(), minimalTurn)
	}

	first := f.call(http.MethodGet, "/sessions?pageSize=2", v2Alice, "")
	last := f.call(http.MethodGet, "/sessions?pageSize=2&pageNumber=2", v2Alice, "")
	past := f.call(http.MethodGet, "/sessions?pageSize=2&pageNumber=5", v2Alice, "")

	if !strings.Contains(first.Body.String(), `"nextPage":2`) {
		t.Errorf("first page has no nextPage 2: %s", first.Body)
	}
	if !strings.Contains(last.Body.String(), `"nextPage":null`) {
		t.Errorf("last page has a nextPage: %s", last.Body)
	}
	if !strings.Contains(past.Body.String(), `"count":3`) {
		t.Errorf("a page past the end reports the wrong count: %s", past.Body)
	}
}

func TestChatV2SearchRequiresAQueryAndFindsByTitle(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	f.save(v2Alice, session.ID, idgen.New(), minimalTurn)
	f.call(http.MethodPatch, "/sessions/"+session.ID, v2Alice, `{"name":"Quarterly report"}`)

	if rec := f.call(http.MethodGet, "/sessions/search", v2Alice, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("search without a query = %d, want 422", rec.Code)
	}
	rec := f.call(http.MethodGet, "/sessions/search?query=quarterly", v2Alice, "")
	if found := decodeData[[]chatv2.Session](t, rec); len(found) != 1 || found[0].ID != session.ID {
		t.Errorf("found %+v, want the renamed session", found)
	}
	other := f.call(http.MethodGet, "/sessions/search?query=quarterly", v2Bob, "")
	if found := decodeData[[]chatv2.Session](t, other); len(found) != 0 {
		t.Errorf("bob found %+v, want none", found)
	}
}

func TestChatV2UpdateSessionChangesOnlyTheFieldsSent(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)

	rec := f.call(http.MethodPatch, "/sessions/"+session.ID, v2Alice, `{"name":"Quarterly"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}
	got := decodeData[chatv2.Session](t, rec)
	if got.Name == nil || *got.Name != "Quarterly" || got.AgentID != nil {
		t.Errorf("session = %+v, want the new name and no agent", got)
	}
	for name, body := range map[string]string{"empty name": `{"name":""}`, "null name": `{"name":null}`, "nothing": `{}`} {
		if rec := f.call(http.MethodPatch, "/sessions/"+session.ID, v2Alice, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %s, want 422", name, rec.Code, rec.Body)
		}
	}
}

func TestChatV2DeleteRemovesTheSession(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)

	if rec := f.call(http.MethodDelete, "/sessions/"+session.ID, v2Alice, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s, want 204", rec.Code, rec.Body)
	}
	if rec := f.call(http.MethodGet, "/sessions/"+session.ID, v2Alice, ""); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", rec.Code)
	}
}

func TestChatV2MarkViewedSetsTheViewTimeAndReturnsTheSession(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	if session.LastViewedTime != 0 {
		t.Fatalf("a new session is already viewed: %+v", session)
	}

	rec := f.call(http.MethodPut, "/sessions/"+session.ID+"/viewed", v2Alice, "")

	if got := decodeData[chatv2.Session](t, rec); rec.Code != http.StatusOK || got.LastViewedTime == 0 {
		t.Errorf("viewed = %d %+v, want 200 with lastViewedTime set", rec.Code, got)
	}
}

func TestChatV2ContextUsageOfAnEmptySessionIsZero(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)

	rec := f.call(http.MethodGet, "/sessions/"+session.ID+"/contextUsage", v2Alice, "")

	got := decodeData[chatv2.ContextUsage](t, rec)
	if rec.Code != http.StatusOK || got.ID != session.ID || got.TotalTasks != 0 || got.MaxInputTokens != nil {
		t.Errorf("usage = %d %+v, want an empty session's zeroes", rec.Code, got)
	}
}

func TestChatV2CompactRefusesASessionWithoutAnAgent(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)

	rec := f.call(http.MethodPut, "/sessions/"+session.ID+"/compaction", v2Alice, `{}`)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d %s, want 502 for a session with no agent", rec.Code, rec.Body)
	}
	if rec := f.call(http.MethodPut, "/sessions/"+session.ID+"/compaction", v2Alice, `{"targetPercent":101}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("targetPercent 101 = %d, want 422", rec.Code)
	}
}

func TestChatV2ReadsHistoryTheWebUiWroteOnV1(t *testing.T) {
	f := newV2Session(t)
	session := f.create(v2Alice)
	chatH := &chatTaskHandler{core: f.a.core, sessRepo: f.sess, chatRepo: f.chat, sseRepo: f.sse, logger: f.a.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)
	body, err := json.Marshal(map[string]any{
		"id":             idgen.New(),
		"messageBubbles": `[{"id":"b1","type":"user","text":"old","files":[{"name":"a.txt"}],"isComplete":true}]`,
		"taskMetadata":   `{"agent_name":"Legacy","status":"completed"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	v1 := withIdentity(httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+session.ID+"/chatTasks", strings.NewReader(string(body))), v2Alice)
	v1rec := httptest.NewRecorder()
	mux.ServeHTTP(v1rec, v1)
	if v1rec.Code != http.StatusOK {
		t.Fatalf("v1 save = %d %s", v1rec.Code, v1rec.Body)
	}

	rec := f.call(http.MethodGet, "/sessions/"+session.ID+"/chatTasks", v2Alice, "")

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"isComplete":true`) || !strings.Contains(rec.Body.String(), `"agent_name":"Legacy"`) {
		t.Fatalf("v2 read of a v1 turn = %d %s, want it back with its keys", rec.Code, rec.Body)
	}
}
