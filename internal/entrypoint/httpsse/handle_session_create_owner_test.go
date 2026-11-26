// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	createOwnerAlice = "alice"
	createOwnerBob   = "bob"
	alicePrivateName = "alice-private-title"
	aliceAgent       = "alice-private-agent"
)

func newSessionCreateMux(t *testing.T, logger *slog.Logger) (*http.ServeMux, *store.SessionRepo, *sessionHandler) {
	t.Helper()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)
	if logger == nil {
		logger = adapter.logger
	}
	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions", sessH.handleCreate)
	mux.HandleFunc("GET /api/v1/sessions/{id}", sessH.handleGet)
	return mux, sessRepo, sessH
}

func postCreateSession(mux http.Handler, userID, sessionID, agentID string) *httptest.ResponseRecorder {
	body, err := json.Marshal(createSessionTestRequest{ID: sessionID, AgentID: agentID})
	if err != nil {
		panic(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, asUser(req, userID))
	return rec
}

func getSessionAs(mux http.Handler, userID, sessionID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, asUser(req, userID))
	return rec
}

func seedAliceSession(t *testing.T, repo *store.SessionRepo, id string) {
	t.Helper()
	name, agent := alicePrivateName, aliceAgent
	if err := repo.Create(t.Context(), &store.Session{ID: id, UserID: createOwnerAlice, Name: &name, AgentID: &agent}); err != nil {
		t.Fatalf("seed alice's session: %v", err)
	}
}

func assertNoSessionData(t *testing.T, body string) {
	t.Helper()
	for _, secret := range []string{alicePrivateName, aliceAgent, createOwnerAlice, "userId", "createdBy", "agentId"} {
		if strings.Contains(body, secret) {
			t.Errorf("response body leaks %q: %s", secret, body)
		}
	}
}

func assertSessionUntouched(t *testing.T, repo *store.SessionRepo, id string) {
	t.Helper()
	got, err := repo.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("alice's session vanished")
	}
	if got.UserID != createOwnerAlice || got.Name == nil || *got.Name != alicePrivateName || got.AgentID == nil || *got.AgentID != aliceAgent {
		t.Errorf("alice's session was modified: %+v", got)
	}
}

func TestHandleCreateSession_ExistingID_ByOwnership(t *testing.T) {
	cases := []struct {
		name       string
		id         func() string
		seed       func(t *testing.T, repo *store.SessionRepo, id string)
		caller     string
		wantStatus int
	}{
		{
			name:       "owner re-create returns own record",
			id:         idgen.New,
			seed:       seedAliceSession,
			caller:     createOwnerAlice,
			wantStatus: http.StatusOK,
		},
		{
			name:       "non-owner gets 404 with no session data",
			id:         idgen.New,
			seed:       seedAliceSession,
			caller:     createOwnerBob,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown id is created for the caller",
			id:         idgen.New,
			seed:       func(*testing.T, *store.SessionRepo, string) {},
			caller:     createOwnerBob,
			wantStatus: http.StatusCreated,
		},
		{
			name: "someone else's soft-deleted id gets 404",
			id:   idgen.New,
			seed: func(t *testing.T, repo *store.SessionRepo, id string) {
				seedAliceSession(t, repo, id)
				softDeleteForTest(t, repo, id, createOwnerAlice)
			},
			caller:     createOwnerBob,
			wantStatus: http.StatusNotFound,
		},
		{
			name: "own soft-deleted id gets 404 (an id a tombstone holds is never reusable), not a 500",
			id:   idgen.New,
			seed: func(t *testing.T, repo *store.SessionRepo, id string) {
				seedAliceSession(t, repo, id)
				softDeleteForTest(t, repo, id, createOwnerAlice)
			},
			caller:     createOwnerAlice,
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, repo, _ := newSessionCreateMux(t, nil)
			id := tc.id()
			tc.seed(t, repo, id)

			rec := postCreateSession(mux, tc.caller, id, "bob-requested-agent")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}

			switch tc.wantStatus {
			case http.StatusNotFound:
				assertNotFoundWithMsg(t, rec, tc.name)
				assertNoSessionData(t, rec.Body.String())
			case http.StatusOK:
				var resp SessionResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if resp.ID != id || resp.UserID != tc.caller {
					t.Errorf("response = {id:%q userId:%q}, want the caller's own record for %q", resp.ID, resp.UserID, id)
				}
				if resp.AgentID == nil || *resp.AgentID != aliceAgent {
					t.Errorf("AgentID = %v, want %s (re-create must not overwrite)", resp.AgentID, aliceAgent)
				}
			case http.StatusCreated:
				got, err := repo.GetByID(t.Context(), id)
				if err != nil || got == nil || got.UserID != tc.caller {
					t.Errorf("persisted session = %+v err=%v, want owned by %s", got, err, tc.caller)
				}
			}

			if tc.wantStatus == http.StatusNotFound && !strings.Contains(tc.name, "soft-deleted") {
				assertSessionUntouched(t, repo, id)
			}
		})
	}
}

func softDeleteForTest(t *testing.T, repo *store.SessionRepo, id, userID string) {
	t.Helper()
	if _, err := repo.SoftDelete(t.Context(), id, userID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
}

func TestHandleCreateSession_NonOwnerAnswerMatchesGet(t *testing.T) {
	mux, repo, _ := newSessionCreateMux(t, nil)
	id := idgen.New()
	seedAliceSession(t, repo, id)

	post := postCreateSession(mux, createOwnerBob, id, "")
	get := getSessionAs(mux, createOwnerBob, id)

	if post.Code != get.Code {
		t.Fatalf("POST status %d != GET status %d", post.Code, get.Code)
	}
	var postErr, getErr ErrorResponse
	if err := json.Unmarshal(post.Body.Bytes(), &postErr); err != nil {
		t.Fatalf("decode POST body: %v", err)
	}
	if err := json.Unmarshal(get.Body.Bytes(), &getErr); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if postErr.Message != getErr.Message {
		t.Errorf("POST message %q != GET message %q", postErr.Message, getErr.Message)
	}
	if postErr.ErrorID == "" {
		t.Error("error envelope must carry errorId")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(post.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decode POST body fields: %v", err)
	}
	for field := range fields {
		if field != "message" && field != "errorId" {
			t.Errorf("unexpected field %q in 404 body", field)
		}
	}
}

func TestHandleCreateSession_LookupErrorIsServerError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)
	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions", sessH.handleCreate)

	id := idgen.New()
	seedAliceSession(t, sessRepo, id)
	if err := adapter.core.Store().Close(); err != nil {
		t.Fatalf("close store to force lookup errors: %v", err)
	}

	for _, caller := range []string{createOwnerAlice, createOwnerBob} {
		t.Run(caller, func(t *testing.T) {
			logs.Reset()
			rec := postCreateSession(mux, caller, id, "")
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
			}
			assertNoSessionData(t, rec.Body.String())
			if strings.Contains(rec.Body.String(), sessionNotFoundMsg) {
				t.Errorf("a lookup failure must not read as not-found: %s", rec.Body.String())
			}
			logged := logs.String()
			if !strings.Contains(logged, `"msg":"create session: lookup existing"`) {
				t.Errorf("lookup failure not logged under its constant message: %s", logged)
			}
			if strings.Contains(logged, "@example.com") {
				t.Errorf("log line carries the caller's email: %s", logged)
			}
		})
	}
}

func TestHandleCreateSession_ConcurrentCreateSameID(t *testing.T) {
	const rounds = 25
	const attemptsPerUser = 3
	mux, repo, _ := newSessionCreateMux(t, nil)

	type result struct {
		user string
		code int
		body string
	}

	for round := range rounds {
		id := idgen.New()
		users := []string{createOwnerAlice, createOwnerBob}
		results := make(chan result, len(users)*attemptsPerUser)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, user := range users {
			for range attemptsPerUser {
				wg.Go(func() {
					<-start
					rec := postCreateSession(mux, user, id, "")
					results <- result{user: user, code: rec.Code, body: rec.Body.String()}
				})
			}
		}
		close(start)
		wg.Wait()
		close(results)

		got, err := repo.GetByID(t.Context(), id)
		if err != nil || got == nil {
			t.Fatalf("round %d: session not persisted: %+v err=%v", round, got, err)
		}
		created := 0
		for res := range results {
			switch {
			case res.code == http.StatusCreated:
				created++
				if res.user != got.UserID {
					t.Errorf("round %d: %s got 201 but %s owns the row", round, res.user, got.UserID)
				}
			case res.user == got.UserID && res.code == http.StatusOK:
			case res.user != got.UserID && res.code == http.StatusNotFound:
				if strings.Contains(res.body, got.UserID) || strings.Contains(res.body, `"userId"`) {
					t.Errorf("round %d: loser's 404 carries the owner's data: %s", round, res.body)
				}
			default:
				t.Errorf("round %d: %s got %d (owner %s), body=%s", round, res.user, res.code, got.UserID, res.body)
			}
		}
		if created != 1 {
			t.Errorf("round %d: %d creates returned 201, want exactly 1", round, created)
		}
	}
}

func TestHandleCreateSession_RejectsNonCanonicalID(t *testing.T) {
	aliceID := idgen.New()
	cases := []struct {
		name string
		id   string
	}{
		{"not a uuid", "not a uuid/../?x=1"},
		{"placeholder string", "undefined"},
		{"uppercase form of alice's id", strings.ToUpper(aliceID)},
		{"braced form of alice's id", "{" + aliceID + "}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, repo, _ := newSessionCreateMux(t, nil)
			seedAliceSession(t, repo, aliceID)

			rec := postCreateSession(mux, createOwnerBob, tc.id, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
			}
			assertNoSessionData(t, rec.Body.String())
			assertSessionUntouched(t, repo, aliceID)
		})
	}
}

func callResolveCreateConflict(h *sessionHandler, userID, sessionID string) *httptest.ResponseRecorder {
	req := asUser(httptest.NewRequest(http.MethodPost, "/api/v1/sessions", nil), userID)
	rec := httptest.NewRecorder()
	created, fail := h.resolveCreateConflict(req.Context(), sessionID, userID)
	if fail != nil {
		fail.write(rec)
		return rec
	}
	writeJSON(rec, http.StatusOK, created.session)
	return rec
}

func TestResolveCreateConflict_AnswersByOwnership(t *testing.T) {
	cases := []struct {
		name       string
		caller     string
		seeded     bool
		wantStatus int
	}{
		{"owner gets own record", createOwnerAlice, true, http.StatusOK},
		{"non-owner gets 404 with no data", createOwnerBob, true, http.StatusNotFound},
		{"row the lookup cannot see gets 404", createOwnerAlice, false, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, repo, h := newSessionCreateMux(t, nil)
			id := idgen.New()
			if tc.seeded {
				seedAliceSession(t, repo, id)
			}

			rec := callResolveCreateConflict(h, tc.caller, id)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusNotFound {
				assertNotFoundWithMsg(t, rec, tc.name)
				assertNoSessionData(t, rec.Body.String())
				return
			}
			var resp SessionResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.ID != id || resp.UserID != tc.caller || resp.AgentID == nil || *resp.AgentID != aliceAgent {
				t.Errorf("response = %+v, want the owner's record for %q", resp, id)
			}
		})
	}
}

func TestResolveCreateConflict_ReReadFailureIsServerError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	_, repo, h := newSessionCreateMux(t, logger)
	id := idgen.New()
	seedAliceSession(t, repo, id)
	if err := h.core.Store().Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	rec := callResolveCreateConflict(h, createOwnerAlice, id)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), sessionNotFoundMsg) {
		t.Errorf("a re-read failure must not read as not-found: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), `"msg":"create session: lookup after conflict"`) {
		t.Errorf("re-read failure not logged under its constant message: %s", logs.String())
	}
}

func failSessionInserts(t *testing.T, h *sessionHandler) {
	t.Helper()
	db := h.core.Store()
	var statements []string
	switch db.Dialect() {
	case "postgres":
		statements = []string{
			`CREATE FUNCTION fail_session_insert() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'forced insert failure'; END $$ LANGUAGE plpgsql`,
			`CREATE TRIGGER fail_session_insert BEFORE INSERT ON sessions FOR EACH ROW EXECUTE FUNCTION fail_session_insert()`,
		}
	default:
		statements = []string{
			`CREATE TRIGGER fail_session_insert BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT, 'forced insert failure'); END`,
		}
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("install failing insert trigger: %v", err)
		}
	}
}

func TestHandleCreateSession_NonUniqueInsertFailureIsServerError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	mux, _, h := newSessionCreateMux(t, logger)
	failSessionInserts(t, h)

	rec := postCreateSession(mux, createOwnerBob, idgen.New(), "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), sessionNotFoundMsg) {
		t.Errorf("an insert failure must not read as not-found: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), `"msg":"create session"`) {
		t.Errorf("insert failure not logged under its constant message: %s", logs.String())
	}
}

func TestHandleCreateSession_ServerErrorIDMatchesLog(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	mux, _, h := newSessionCreateMux(t, logger)
	failSessionInserts(t, h)

	rec := postCreateSession(mux, createOwnerBob, idgen.New(), "")
	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.ErrorID == "" {
		t.Fatalf("decode error envelope: %v body=%s", err, rec.Body.String())
	}
	if !strings.Contains(logs.String(), `"errorId":"`+resp.ErrorID+`"`) {
		t.Errorf("errorId %q not found in the handler's log: %s", resp.ErrorID, logs.String())
	}
}
