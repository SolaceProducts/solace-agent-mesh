// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type shareWiring struct {
	t         *testing.T
	adapter   *HTTPSSEAdapter
	core      *entrypoint.GatewayCore
	arts      *artifact.ScopedService
	sessions  *store.SessionRepo
	chats     *store.ChatTaskRepo
	sessionID string
	shareURL  string
}

func newShareWiring(t *testing.T, dbName string) *shareWiring {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service":       map[string]any{"type": "sql", "database_url": testDBURL(t, dbName)},
	})
	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	arts := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(flags), runtime.WithArtifacts(arts)).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Store().Close() })
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })
	return &shareWiring{t: t, adapter: adapter, core: core, arts: arts, sessions: store.NewSessionRepo(core.Store()), chats: store.NewChatTaskRepo(core.Store())}
}

func (w *shareWiring) seedSharedChat() {
	w.t.Helper()
	w.sessionID = idgen.New()
	name := "Shared chat"
	if err := w.sessions.Create(w.t.Context(), &store.Session{ID: w.sessionID, UserID: createOwnerAlice, Name: &name}); err != nil {
		w.t.Fatalf("seed session: %v", err)
	}
	if err := w.chats.Create(w.t.Context(), &store.ChatTask{
		ID: idgen.New(), SessionID: w.sessionID, UserID: createOwnerAlice,
		UserMessage: strPtr("make the files"), MessageBubbles: bubblesReferencing(referencedName), CreatedTime: 1000,
	}); err != nil {
		w.t.Fatalf("seed chat task: %v", err)
	}
	w.save(createOwnerAlice, w.sessionID, sessionScopedName, sessionScopedBytes)
	w.save(createOwnerAlice, w.sessionID, referencedName, referencedBytes)
	w.save(createOwnerAlice, savedElsewhere, unreferencedName, unreferencedBytes)
	w.shareURL = "/api/v1/share/" + w.share(w.sessionID, createOwnerAlice)
}

func (w *shareWiring) save(user, session, filename, content string) {
	w.t.Helper()
	if _, err := w.arts.Inner.Save(w.t.Context(), w.arts.Resolve("", user, session, filename), []byte(content), "text/plain"); err != nil {
		w.t.Fatalf("save %s: %v", filename, err)
	}
}

func (w *shareWiring) serve(method, target, user, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("X-SAM-User-ID", user)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	w.adapter.Handler().ServeHTTP(rec, req)
	return rec
}

func (w *shareWiring) share(sessionID, user string) string {
	w.t.Helper()
	rec := w.serve(http.MethodPost, "/api/v1/share/"+sessionID, user, "")
	var link SharedLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil || rec.Code != http.StatusCreated {
		w.t.Fatalf("create share: %d %s", rec.Code, rec.Body.String())
	}
	return link.ID
}

func (w *shareWiring) listing(shareURL, user string) []string {
	w.t.Helper()
	rec := w.serve(http.MethodGet, shareURL, user, "")
	var view SharedSessionView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || rec.Code != http.StatusOK {
		w.t.Fatalf("view as %s: %d %s", user, rec.Code, rec.Body.String())
	}
	names := make([]string, 0, len(view.Artifacts))
	for _, a := range view.Artifacts {
		names = append(names, a.Filename)
	}
	slices.Sort(names)
	return names
}

func (w *shareWiring) open(shareURL, user, filename string) *httptest.ResponseRecorder {
	return w.serve(http.MethodGet, shareURL+"/artifacts/"+url.PathEscape(filename), user, "")
}

func (w *shareWiring) forkedFiles(shareURL, user string) []string {
	w.t.Helper()
	rec := w.serve(http.MethodPost, shareURL+"/fork", user, "")
	var forked ForkSharedChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &forked); err != nil || rec.Code != http.StatusOK {
		w.t.Fatalf("fork as %s: %d %s", user, rec.Code, rec.Body.String())
	}
	keys, err := w.arts.Inner.ListKeys(w.t.Context(), artifact.KeyScope{AppName: "test-ns", UserID: user, SessionID: forked.SessionID})
	if err != nil {
		w.t.Fatalf("list forked scope: %v", err)
	}
	return slices.DeleteFunc(keys, artifact.IsInternalArtifact)
}

func TestSharedChatShowsAViewerOnlyTheUserScopedFilesItReferencesThroughTheAdapterRoutes(t *testing.T) {
	w := newShareWiring(t, "sharescope")
	w.seedSharedChat()

	t.Run("the viewer is shown the ordinary file and the referenced user-scoped file", func(t *testing.T) {
		if got := w.listing(w.shareURL, createOwnerBob); !slices.Equal(got, []string{sessionScopedName, referencedName}) {
			t.Errorf("viewer is shown %v", got)
		}
	})
	t.Run("the sharer is shown every file", func(t *testing.T) {
		if got := w.listing(w.shareURL, createOwnerAlice); len(got) != 3 {
			t.Errorf("sharer is shown %v, want three files", got)
		}
	})
	t.Run("the viewer opens the referenced user-scoped file and is refused the other", func(t *testing.T) {
		if rec := w.open(w.shareURL, createOwnerBob, referencedName); rec.Code != http.StatusOK || rec.Body.String() != referencedBytes {
			t.Errorf("referenced file: %d %q, want 200 %q", rec.Code, rec.Body.String(), referencedBytes)
		}
		rec := w.open(w.shareURL, createOwnerBob, unreferencedName)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), unreferencedBytes) {
			t.Errorf("unreferenced file: %d %q, want 404 without the bytes", rec.Code, rec.Body.String())
		}
		if rec = w.open(w.shareURL, createOwnerAlice, unreferencedName); rec.Code != http.StatusOK || rec.Body.String() != unreferencedBytes {
			t.Errorf("the sharer: %d %q, want 200 %q", rec.Code, rec.Body.String(), unreferencedBytes)
		}
	})
	t.Run("the viewer's fork holds the ordinary file and the referenced user-scoped file", func(t *testing.T) {
		if got := w.forkedFiles(w.shareURL, createOwnerBob); !slices.Equal(got, []string{sessionScopedName, referencedName}) {
			t.Errorf("fork holds %v", got)
		}
	})
}

func TestSharedChatRefusesAVersionAnotherSessionWroteThroughTheAdapterRoutes(t *testing.T) {
	w := newShareWiring(t, "sharelater")
	w.seedSharedChat()
	w.save(createOwnerAlice, savedElsewhere, referencedName, laterVersionBytes)

	rec := w.open(w.shareURL, createOwnerBob, referencedName)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), laterVersionBytes) {
		t.Errorf("open: %d %q, want 404 without the later version", rec.Code, rec.Body.String())
	}
	if got := w.listing(w.shareURL, createOwnerBob); !slices.Equal(got, []string{sessionScopedName}) {
		t.Errorf("viewer is shown %v, want only the ordinary file", got)
	}
	if got := w.forkedFiles(w.shareURL, createOwnerBob); !slices.Equal(got, []string{sessionScopedName}) {
		t.Errorf("fork holds %v, want only the ordinary file", got)
	}
}

func TestSharedChatAnswersAnOutageWithAServerErrorThroughTheAdapterRoutes(t *testing.T) {
	w := newShareWiring(t, "shareoutage")
	w.seedSharedChat()
	if _, err := w.core.Store().ExecContext(t.Context(), `DROP TABLE chat_tasks`); err != nil {
		t.Fatalf("break the chat store: %v", err)
	}

	rec := w.open(w.shareURL, createOwnerBob, referencedName)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), referencedBytes) {
		t.Errorf("open a user-scoped file: %d %q, want 500 and no bytes", rec.Code, rec.Body.String())
	}
	if rec = w.open(w.shareURL, createOwnerBob, sessionScopedName); rec.Code != http.StatusOK || rec.Body.String() != sessionScopedBytes {
		t.Errorf("open an ordinary file: %d %q, want 200", rec.Code, rec.Body.String())
	}
}

func TestChatTaskSaveStoresTheClientsMetadataThroughTheAdapterRoutes(t *testing.T) {
	w := newShareWiring(t, "savemetadata")
	sessionID := idgen.New()
	name := "A chat"
	if err := w.sessions.Create(t.Context(), &store.Session{ID: sessionID, UserID: createOwnerAlice, Name: &name}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	taskID := idgen.New()
	save := func(metadata string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": taskID, "messageBubbles": `[]`, "taskMetadata": metadata})
		if rec := w.serve(http.MethodPost, "/api/v1/sessions/"+sessionID+"/chatTasks", createOwnerAlice, string(body)); rec.Code != http.StatusOK {
			t.Fatalf("save chat task: %d %s", rec.Code, rec.Body.String())
		}
	}
	stored := func() string {
		t.Helper()
		task, err := w.chats.GetByID(t.Context(), taskID)
		if err != nil || task == nil || task.TaskMetadata == nil {
			t.Fatalf("stored task = %v (err %v), want metadata", task, err)
		}
		return *task.TaskMetadata
	}

	first := `{"schemaVersion":1,"status":"pending","ragData":[{"text":"<b>"}]}`
	save(first)
	if got := stored(); got != first {
		t.Errorf("first save stored %s, want %s", got, first)
	}
	second := `{"schemaVersion":1,"status":"completed"}`
	save(second)
	if got := stored(); got != second {
		t.Errorf("second save stored %s, want %s", got, second)
	}
}

func TestSharedForkOfAnotherUsersChatDoesNotVouchThroughTheAdapterRoutes(t *testing.T) {
	const author, forker = createOwnerBob, createOwnerAlice
	w := newShareWiring(t, "sharefork")

	authorSession := idgen.New()
	name := "Author's chat"
	if err := w.sessions.Create(t.Context(), &store.Session{ID: authorSession, UserID: author, Name: &name}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := w.chats.Create(t.Context(), &store.ChatTask{
		ID: idgen.New(), SessionID: authorSession, UserID: author,
		UserMessage: strPtr("hi"), MessageBubbles: bubblesReferencing(unreferencedName), CreatedTime: 1000,
	}); err != nil {
		t.Fatalf("seed author's task: %v", err)
	}
	w.save(forker, savedElsewhere, unreferencedName, unreferencedBytes)

	rec := w.serve(http.MethodPost, "/api/v1/share/"+w.share(authorSession, author)+"/fork", forker, "")
	var forked ForkSharedChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &forked); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("fork: %d %s", rec.Code, rec.Body.String())
	}
	forkShareURL := "/api/v1/share/" + w.share(forked.SessionID, forker)

	opensForkersFile := func() *httptest.ResponseRecorder { return w.open(forkShareURL, author, unreferencedName) }
	if rec := opensForkersFile(); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), unreferencedBytes) {
		t.Errorf("author opens the forker's file through the shared fork: %d %q, want 404 without the bytes", rec.Code, rec.Body.String())
	}
	if got := w.listing(forkShareURL, author); slices.Contains(got, unreferencedName) {
		t.Errorf("the shared fork lists the forker's file: %v", got)
	}

	t.Run("saving the forked task again keeps it from vouching", func(t *testing.T) {
		tasks, err := w.chats.ListBySession(t.Context(), forked.SessionID)
		if err != nil || len(tasks) != 1 {
			t.Fatalf("forked tasks = %v (err %v), want one", tasks, err)
		}
		body, _ := json.Marshal(map[string]any{
			"id": tasks[0].ID, "messageBubbles": tasks[0].MessageBubbles,
			"taskMetadata": `{"schemaVersion":1,"status":"completed"}`,
		})
		if rec := w.serve(http.MethodPost, "/api/v1/sessions/"+forked.SessionID+"/chatTasks", forker, string(body)); rec.Code != http.StatusOK {
			t.Fatalf("save chat task: %d %s", rec.Code, rec.Body.String())
		}
		saved, err := w.chats.GetByID(t.Context(), tasks[0].ID)
		if err != nil || saved == nil || saved.TaskMetadata == nil || !strings.Contains(*saved.TaskMetadata, `"status":"completed"`) || !strings.Contains(*saved.TaskMetadata, entrypoint.ForkedFromOwnerKey) {
			t.Fatalf("saved metadata = %v (err %v), want the client's keys and the fork marker", saved, err)
		}
		if rec := opensForkersFile(); rec.Code != http.StatusNotFound {
			t.Errorf("after the re-save the author opens the forker's file: %d, want 404", rec.Code)
		}
	})

	t.Run("a failed read of the stored task does not become a save that drops the markers", func(t *testing.T) {
		if w.core.Store().Dialect() != "sqlite" {
			t.Skip("the read failure is injected with a text in an integer column, which only SQLite stores; the SQLite run covers the branch")
		}
		tasks, err := w.chats.ListBySession(t.Context(), forked.SessionID)
		if err != nil || len(tasks) != 1 {
			t.Fatalf("forked tasks = %v (err %v), want one", tasks, err)
		}
		if _, err := w.core.Store().ExecContext(t.Context(), `UPDATE chat_tasks SET created_time = 'not-a-number' WHERE id = ?`, tasks[0].ID); err != nil {
			t.Fatalf("corrupt the stored task: %v", err)
		}
		body, _ := json.Marshal(map[string]any{
			"id": tasks[0].ID, "messageBubbles": `[]`, "taskMetadata": `{"schemaVersion":2}`,
		})
		rec := w.serve(http.MethodPost, "/api/v1/sessions/"+forked.SessionID+"/chatTasks", forker, string(body))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("save chat task: %d %s, want 500", rec.Code, rec.Body.String())
		}
		var stored string
		if err := w.core.Store().QueryRowContext(t.Context(), `SELECT task_metadata FROM chat_tasks WHERE id = ?`, tasks[0].ID).Scan(&stored); err != nil {
			t.Fatalf("read the stored metadata: %v", err)
		}
		if !strings.Contains(stored, entrypoint.ForkedFromOwnerKey) || strings.Contains(stored, `"schemaVersion":2`) {
			t.Errorf("stored metadata = %s, want the fork marker and nothing from the refused save", stored)
		}
	})
}
