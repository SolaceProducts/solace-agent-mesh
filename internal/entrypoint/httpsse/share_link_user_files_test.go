// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	shareSharer = "user-1"
	shareViewer = "user-2"
	shareEditor = "user-3"

	sessionScopedName  = "notes.txt"
	referencedName     = "user:report.md"
	unreferencedName   = "user:private.md"
	absentUserName     = "user:nothing-here.md"
	sessionScopedBytes = "SESSION-NOTES-BYTES"
	referencedBytes    = "REFERENCED-USER-FILE-BYTES"
	unreferencedBytes  = "UNREFERENCED-USER-FILE-SECRET"
	laterVersionBytes  = "LATER-VERSION-WRITTEN-ELSEWHERE"
	savedElsewhere     = "another-session-of-the-sharer"
)

func bubblesReferencingVersion(filename string, version int) string {
	bubbles := []map[string]any{
		{"id": "m1", "type": "user", "text": "make the files"},
		{
			"id": "m2", "type": "agent",
			"text": "done«artifact_return:" + filename + "»",
			"parts": []map[string]any{
				{"kind": "text", "text": "done"},
				{"kind": "artifact", "status": "completed", "name": filename, "file": map[string]any{
					"name": filename, "uri": "artifact://s/" + url.PathEscape(filename) + "?version=" + strconv.Itoa(version),
				}},
			},
		},
	}
	raw, err := json.Marshal(bubbles)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func bubblesReferencing(filename string) string { return bubblesReferencingVersion(filename, 0) }

type flakyArtifactBackend struct {
	artifact.Service
	failVersions   atomic.Bool
	failKeys       atomic.Bool
	failVersionsOf atomic.Pointer[string]

	afterVersionLookup atomic.Pointer[func(loc artifact.Locator)]

	mu           sync.Mutex
	versionAsked map[string]int
	loaded       map[string]*int
}

func (f *flakyArtifactBackend) versionLookups(filename string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.versionAsked[filename]
}

func (f *flakyArtifactBackend) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	f.mu.Lock()
	if f.loaded == nil {
		f.loaded = map[string]*int{}
	}
	f.loaded[loc.Filename] = version
	f.mu.Unlock()
	return f.Service.Load(ctx, loc, version)
}

func (f *flakyArtifactBackend) versionLoaded(filename string) *int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loaded[filename]
}

func (f *flakyArtifactBackend) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	f.mu.Lock()
	if f.versionAsked == nil {
		f.versionAsked = map[string]int{}
	}
	f.versionAsked[loc.Filename]++
	f.mu.Unlock()
	if hook := f.afterVersionLookup.Load(); hook != nil && loc.Filename == referencedName {
		versions, err := f.Service.ListVersions(ctx, loc)
		f.afterVersionLookup.Store(nil)
		(*hook)(loc)
		return versions, err
	}
	if failing := f.failVersionsOf.Load(); failing != nil && *failing == loc.Filename {
		return nil, errors.New("artifact store unavailable")
	}
	if f.failVersions.Load() {
		return nil, errors.New("artifact store unavailable")
	}
	return f.Service.ListVersions(ctx, loc)
}

func (f *flakyArtifactBackend) ListArtifactVersions(ctx context.Context, scope artifact.KeyScope) (map[string][]int, error) {
	keys, err := f.Service.ListKeys(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]int, len(keys))
	for _, key := range keys {
		versions, err := f.Service.ListVersions(ctx, artifact.Locator{AppName: scope.AppName, UserID: scope.UserID, SessionID: scope.SessionID, Filename: key})
		if err != nil {
			return nil, err
		}
		out[key] = versions
	}
	return out, nil
}

func (f *flakyArtifactBackend) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	if f.failKeys.Load() {
		return nil, errors.New("artifact store unavailable")
	}
	return f.Service.ListKeys(ctx, scope)
}

type shareUserFilesFixture struct {
	t         *testing.T
	h         *shareLinkHandler
	mux       *http.ServeMux
	sessRepo  *store.SessionRepo
	chatRepo  *store.ChatTaskRepo
	arts      *artifact.ScopedService
	backend   *flakyArtifactBackend
	logs      *warnRecorder
	sessionID string
	shareID   string
}

func newShareUserFilesFixture(t *testing.T, bubbles string) *shareUserFilesFixture {
	t.Helper()
	h, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	logs := &warnRecorder{}
	h.logger = logs.logger()
	backend := &flakyArtifactBackend{Service: filesystem.New(t.TempDir())}
	arts := &artifact.ScopedService{Inner: backend, Scope: "namespace", Namespace: "test-ns"}
	h.artSvc = arts

	sessionID := idgen.New()
	seedSession(t, sessRepo, shareSharer, sessionID, "Shared chat")
	f := &shareUserFilesFixture{t: t, h: h, mux: mux, sessRepo: sessRepo, chatRepo: chatRepo, arts: arts, backend: backend, logs: logs, sessionID: sessionID}
	f.addTask(shareSharer, bubbles, nil)
	f.save(shareSharer, sessionID, sessionScopedName, sessionScopedBytes)
	f.save(shareSharer, sessionID, referencedName, referencedBytes)
	f.save(shareSharer, savedElsewhere, unreferencedName, unreferencedBytes)

	f.shareID = createShareAs(t, mux, sessionID, shareSharer, "sharer@example.com", "").ID
	logs.reset()
	return f
}

func (f *shareUserFilesFixture) addTask(user, bubbles string, metadata *string) {
	f.t.Helper()
	if err := f.chatRepo.Create(f.t.Context(), &store.ChatTask{
		ID: idgen.New(), SessionID: f.sessionID, UserID: user,
		UserMessage: strPtr("a turn"), MessageBubbles: bubbles, TaskMetadata: metadata, CreatedTime: 1000,
	}); err != nil {
		f.t.Fatalf("seed chat task: %v", err)
	}
}

func (f *shareUserFilesFixture) save(user, session, filename, content string) {
	f.t.Helper()
	loc := f.arts.Resolve("", user, session, filename)
	if _, err := f.arts.Inner.Save(f.t.Context(), loc, []byte(content), "text/plain"); err != nil {
		f.t.Fatalf("save %s: %v", filename, err)
	}
}

func (f *shareUserFilesFixture) serve(method, target, user string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, reqWithUser(method, target, "", user, user+"@example.com"))
	return rec
}

func (f *shareUserFilesFixture) viewAs(user string) *httptest.ResponseRecorder {
	return f.serve(http.MethodGet, "/api/v1/share/"+f.shareID, user)
}

func (f *shareUserFilesFixture) listing(user string) []string {
	f.t.Helper()
	rec := f.viewAs(user)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("view as %s: %d %s", user, rec.Code, rec.Body.String())
	}
	var view SharedSessionView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		f.t.Fatalf("decode view: %v", err)
	}
	names := make([]string, 0, len(view.Artifacts))
	for _, a := range view.Artifacts {
		names = append(names, a.Filename)
	}
	slices.Sort(names)
	return names
}

func (f *shareUserFilesFixture) open(user, filename string) *httptest.ResponseRecorder {
	return f.serve(http.MethodGet, "/api/v1/share/"+f.shareID+"/artifacts/"+url.PathEscape(filename), user)
}

func (f *shareUserFilesFixture) forkAs(user string) *httptest.ResponseRecorder {
	return f.serve(http.MethodPost, "/api/v1/share/"+f.shareID+"/fork", user)
}

func (f *shareUserFilesFixture) fork(user string) []string {
	f.t.Helper()
	rec := f.forkAs(user)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("fork as %s: %d %s", user, rec.Code, rec.Body.String())
	}
	var resp ForkSharedChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		f.t.Fatalf("decode fork: %v", err)
	}
	keys, err := f.arts.Inner.ListKeys(f.t.Context(), artifact.KeyScope{AppName: "test-ns", UserID: user, SessionID: resp.SessionID})
	if err != nil {
		f.t.Fatalf("list forked scope: %v", err)
	}
	return slices.DeleteFunc(keys, artifact.IsInternalArtifact)
}

func (f *shareUserFilesFixture) sessionCount(user string) int {
	f.t.Helper()
	count, err := f.sessRepo.CountByUser(f.t.Context(), user, store.SessionListFilter{IncludeDeleted: true})
	if err != nil {
		f.t.Fatalf("count sessions: %v", err)
	}
	return count
}

func (f *shareUserFilesFixture) logged(level string) []map[string]any {
	var out []map[string]any
	for _, line := range f.logs.lines() {
		if line["level"] == level {
			out = append(out, line)
		}
	}
	return out
}

func shareErrorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Message
}

func TestShareLinkViewerIsShownOnlyTheUserScopedFilesTheChatReferences(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))

	if got := f.listing(shareViewer); !slices.Equal(got, []string{sessionScopedName, referencedName}) {
		t.Errorf("viewer is shown %v, want the ordinary file and the referenced user-scoped file", got)
	}
	if got := f.listing(shareSharer); !slices.Equal(got, []string{sessionScopedName, unreferencedName, referencedName}) {
		t.Errorf("sharer is shown %v, want every file", got)
	}
}

func TestShareLinkViewerOpensOnlyTheUserScopedFilesTheChatReferences(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))

	for _, tc := range []struct {
		name, user, filename string
		status               int
		body                 string
	}{
		{"viewer opens an ordinary file", shareViewer, sessionScopedName, http.StatusOK, sessionScopedBytes},
		{"viewer opens the referenced user-scoped file", shareViewer, referencedName, http.StatusOK, referencedBytes},
		{"sharer opens the referenced user-scoped file", shareSharer, referencedName, http.StatusOK, referencedBytes},
		{"sharer opens the unreferenced user-scoped file", shareSharer, unreferencedName, http.StatusOK, unreferencedBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.open(tc.user, tc.filename)
			if rec.Code != tc.status || rec.Body.String() != tc.body {
				t.Errorf("status = %d body = %q, want %d %q", rec.Code, rec.Body.String(), tc.status, tc.body)
			}
		})
	}

	t.Run("viewer is refused the unreferenced user-scoped file", func(t *testing.T) {
		rec := f.open(shareViewer, unreferencedName)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), unreferencedBytes) {
			t.Fatalf("status = %d body = %q, want 404 without the bytes", rec.Code, rec.Body.String())
		}
		if got := shareErrorMessage(t, rec); got != artifactNotFoundMsg {
			t.Errorf("message = %q, want %q", got, artifactNotFoundMsg)
		}
	})

	t.Run("a user-scoped file that does not exist is answered the same", func(t *testing.T) {
		refused, absent := f.open(shareViewer, unreferencedName), f.open(shareViewer, absentUserName)
		if refused.Code != absent.Code || shareErrorMessage(t, refused) != shareErrorMessage(t, absent) {
			t.Errorf("refused: %d %q, absent: %d %q, want the same answer", refused.Code, shareErrorMessage(t, refused), absent.Code, shareErrorMessage(t, absent))
		}
	})
}

func TestShareLinkForkCopiesOnlyTheUserScopedFilesTheChatReferences(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))

	if got := f.fork(shareViewer); !slices.Equal(got, []string{sessionScopedName, referencedName}) {
		t.Errorf("viewer's fork holds %v, want the ordinary file and the referenced user-scoped file", got)
	}
	if got := f.fork(shareSharer); !slices.Equal(got, []string{sessionScopedName, unreferencedName, referencedName}) {
		t.Errorf("sharer's fork holds %v, want every file", got)
	}
}

func TestShareLinkUserScopedFileIsVouchedForByAnyWayTheChatNamesItsVersion(t *testing.T) {
	asURI := `[{"id":"m1","type":"agent","text":"","parts":[{"kind":"file","file":{"name":"x.md","uri":"artifact://app/user-1/s/user%3Areport.md?version=0"}}]}]`
	asMarkerWithVersion := `[{"id":"m1","type":"agent","text":"see «artifact:user:report.md:0»"}]`
	asAttachment := `[{"id":"m1","type":"user","text":"use this","attachedArtifacts":[{"uri":"artifact://app/user-1/s/user:report.md?version=0","filename":"user:report.md","mimeType":"text/plain"}]}]`
	asNotification := `[{"id":"m1","type":"agent","text":"","artifactNotification":{"name":"user:report.md","version":0}}]`

	for name, bubbles := range map[string]string{
		"artifact URI of a file part":  asURI,
		"inline marker with a version": asMarkerWithVersion,
		"attached artifact":            asAttachment,
		"artifact notification":        asNotification,
		"the chat UI's own turn":       bubblesReferencing(referencedName),
	} {
		t.Run(name, func(t *testing.T) {
			f := newShareUserFilesFixture(t, bubbles)
			if rec := f.open(shareViewer, referencedName); rec.Code != http.StatusOK || rec.Body.String() != referencedBytes {
				t.Errorf("referenced file: %d %q, want 200 %q", rec.Code, rec.Body.String(), referencedBytes)
			}
			if rec := f.open(shareViewer, unreferencedName); rec.Code != http.StatusNotFound {
				t.Errorf("unreferenced file: %d, want 404", rec.Code)
			}
		})
	}

	t.Run("a later task of the same sharer", func(t *testing.T) {
		f := newShareUserFilesFixture(t, `[]`)
		f.addTask(shareSharer, bubblesReferencing(referencedName), nil)
		if rec := f.open(shareViewer, referencedName); rec.Code != http.StatusOK {
			t.Errorf("referenced file: %d, want 200", rec.Code)
		}
	})
}

func TestShareLinkAReferenceThatNamesNoVersionVouchesForNothing(t *testing.T) {
	for name, bubbles := range map[string]string{
		"a bare marker":         `[{"id":"m1","type":"agent","text":"done«artifact_return:user:report.md»"}]`,
		"an artifact part name": `[{"id":"m1","type":"agent","parts":[{"kind":"artifact","name":"user:report.md"}]}]`,
		"an unversioned URI":    `[{"id":"m1","type":"agent","parts":[{"kind":"file","file":{"name":"user:report.md","uri":"artifact://s/user:report.md"}}]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newShareUserFilesFixture(t, bubbles)
			if rec := f.open(shareViewer, referencedName); rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 for a file the chat names at no version", rec.Code)
			}
			if got := f.listing(shareViewer); !slices.Equal(got, []string{sessionScopedName}) {
				t.Errorf("viewer is shown %v, want only the ordinary file", got)
			}
		})
	}
}

func TestShareLinkAVersionWrittenFromAnotherSessionIsNotServedOrForked(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	f.save(shareSharer, savedElsewhere, referencedName, laterVersionBytes)

	rec := f.open(shareViewer, referencedName)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), laterVersionBytes) {
		t.Errorf("open: %d %q, want 404 without the later version", rec.Code, rec.Body.String())
	}
	if got := f.listing(shareViewer); !slices.Equal(got, []string{sessionScopedName}) {
		t.Errorf("viewer is shown %v, want only the ordinary file", got)
	}
	if got := f.fork(shareViewer); !slices.Equal(got, []string{sessionScopedName}) {
		t.Errorf("fork holds %v, want only the ordinary file", got)
	}
	if rec := f.open(shareSharer, referencedName); rec.Code != http.StatusOK || rec.Body.String() != laterVersionBytes {
		t.Errorf("the sharer: %d %q, want 200 with the later version", rec.Code, rec.Body.String())
	}

	t.Run("a message that names the later version vouches for it", func(t *testing.T) {
		f.addTask(shareSharer, bubblesReferencingVersion(referencedName, 1), nil)
		if rec := f.open(shareViewer, referencedName); rec.Code != http.StatusOK || rec.Body.String() != laterVersionBytes {
			t.Errorf("open: %d %q, want 200 %q", rec.Code, rec.Body.String(), laterVersionBytes)
		}
	})
}

func TestShareLinkAnotherUsersMessageDoesNotVouchForTheSharersUserScopedFile(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	f.addTask(shareEditor, bubblesReferencing(unreferencedName), nil)

	if rec := f.open(shareViewer, unreferencedName); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a file only a collaborator's message references", rec.Code)
	}
	if got := f.listing(shareViewer); slices.Contains(got, unreferencedName) {
		t.Errorf("viewer is shown %v, which holds a file only a collaborator's message references", got)
	}
	if got := f.fork(shareViewer); slices.Contains(got, unreferencedName) {
		t.Errorf("fork holds %v, which holds a file only a collaborator's message references", got)
	}
}

func TestShareLinkOnlyStructuredReferencesVouch(t *testing.T) {
	ragMetadata := `{"schemaVersion":1,"ragData":[{"text":"«artifact:user:private.md:0»","uri":"artifact://s/user:private.md?version=0"}]}`
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	f.addTask(shareSharer,
		`[{"id":"m1","type":"agent","text":"","thinkingContent":"«artifact:user:private.md:0»","progressUpdates":[{"text":"«artifact:user:private.md:0»"}],"contextQuote":"«artifact:user:private.md:0»"}]`,
		&ragMetadata)

	if rec := f.open(shareViewer, unreferencedName); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), unreferencedBytes) {
		t.Errorf("status = %d, want 404: a retrieved snippet, thinking and progress text name no file the chat shows", rec.Code)
	}
	if got := f.listing(shareViewer); slices.Contains(got, unreferencedName) {
		t.Errorf("viewer is shown %v", got)
	}
}

func TestShareLinkATaskAForkCopiedDoesNotVouchForTheSharersUserScopedFile(t *testing.T) {
	forked := `{"forkedFromOwnerId":"user-2","forkedFrom":"t1","forkedFromSessionId":"s1"}`
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	f.addTask(shareSharer, bubblesReferencing(unreferencedName), &forked)

	if rec := f.open(shareViewer, unreferencedName); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a file only a forked-in message references", rec.Code)
	}
}

func TestShareLinkAForkOfAnotherUsersChatDoesNotVouchWhenSharedBack(t *testing.T) {
	const author, forker = shareViewer, shareSharer
	h, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	logs := &warnRecorder{}
	h.logger = logs.logger()
	arts := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	h.artSvc = arts

	authorSession := idgen.New()
	seedSession(t, sessRepo, author, authorSession, "Author's chat")
	if err := chatRepo.Create(t.Context(), &store.ChatTask{
		ID: idgen.New(), SessionID: authorSession, UserID: author,
		UserMessage: strPtr("hi"), MessageBubbles: bubblesReferencing(unreferencedName), CreatedTime: 1000,
	}); err != nil {
		t.Fatalf("seed author's task: %v", err)
	}
	loc := arts.Resolve("", forker, savedElsewhere, unreferencedName)
	if _, err := arts.Inner.Save(t.Context(), loc, []byte(unreferencedBytes), "text/plain"); err != nil {
		t.Fatalf("save the forker's file: %v", err)
	}

	authorShare := createShareAs(t, mux, authorSession, author, author+"@example.com", "")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser(http.MethodPost, "/api/v1/share/"+authorShare.ID+"/fork", "", forker, forker+"@example.com"))
	var forked ForkSharedChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &forked); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("fork: %d %s", rec.Code, rec.Body.String())
	}
	forkShare := createShareAs(t, mux, forked.SessionID, forker, forker+"@example.com", "")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser(http.MethodGet, "/api/v1/share/"+forkShare.ID+"/artifacts/"+url.PathEscape(unreferencedName), "", author, author+"@example.com"))
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), unreferencedBytes) {
		t.Errorf("author opens the forker's file through the shared fork: %d %q, want 404 without the bytes", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser(http.MethodGet, "/api/v1/share/"+forkShare.ID, "", author, author+"@example.com"))
	if strings.Contains(rec.Body.String(), `"filename":"`+unreferencedName+`"`) {
		t.Errorf("the shared fork lists the forker's file: %s", rec.Body.String())
	}
}

func TestShareLinkOneUnreadableTaskDoesNotRefuseWhatTheOthersReference(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	f.addTask(shareSharer, `[{"id": "not json`, nil)

	if got := f.listing(shareViewer); !slices.Equal(got, []string{sessionScopedName, referencedName}) {
		t.Errorf("viewer is shown %v, want the ordinary file and the referenced user-scoped file", got)
	}
	if rec := f.open(shareViewer, referencedName); rec.Code != http.StatusOK || rec.Body.String() != referencedBytes {
		t.Errorf("referenced file: %d %q, want 200", rec.Code, rec.Body.String())
	}
	if rec := f.open(shareViewer, unreferencedName); rec.Code != http.StatusNotFound {
		t.Errorf("unreferenced file: %d, want 404", rec.Code)
	}
	if got := f.fork(shareViewer); !slices.Equal(got, []string{sessionScopedName, referencedName}) {
		t.Errorf("fork holds %v", got)
	}
}

func TestShareLinkWarnsOnceAboutAnUnreadableTaskWithIdsOnly(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
	f.addTask(shareSharer, `[{"id": "not json`, nil)

	f.listing(shareViewer)
	warnings := f.logged("WARN")
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings in one request, want 1: %s", len(warnings), f.logs.text())
	}
	if warnings[0]["shareID"] != f.shareID || warnings[0]["userID"] != shareViewer {
		t.Errorf("warning fields = %v, want the share id and the viewer's id", warnings[0])
	}
	if text := f.logs.text(); strings.Contains(text, referencedName) || strings.Contains(text, unreferencedName) || strings.Contains(text, "not json") {
		t.Errorf("the log names a file or task content: %s", text)
	}
}

func TestShareLinkAnOutageIsAnErrorNotARefusal(t *testing.T) {
	t.Run("the chat store fails while a user-scoped file is opened", func(t *testing.T) {
		f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
		if _, err := f.h.core.Store().ExecContext(t.Context(), `DROP TABLE chat_tasks`); err != nil {
			t.Fatalf("break the chat store: %v", err)
		}

		rec := f.open(shareViewer, referencedName)
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), referencedBytes) {
			t.Errorf("status = %d body = %q, want 500 and no bytes", rec.Code, rec.Body.String())
		}
		if got := f.logged("ERROR"); len(got) < 1 {
			t.Errorf("no error logged: %s", f.logs.text())
		}
		for _, line := range f.logged("WARN") {
			if strings.Contains(line["msg"].(string), "refused") {
				t.Errorf("an outage was logged as a refusal: %v", line)
			}
		}
		if rec := f.open(shareViewer, sessionScopedName); rec.Code != http.StatusOK || rec.Body.String() != sessionScopedBytes {
			t.Errorf("an ordinary file: %d %q, want 200", rec.Code, rec.Body.String())
		}
	})

	t.Run("the artifact store cannot list versions", func(t *testing.T) {
		f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
		f.backend.failVersions.Store(true)

		if rec := f.open(shareViewer, referencedName); rec.Code != http.StatusInternalServerError {
			t.Errorf("open: status = %d, want 500", rec.Code)
		}
		if rec := f.open(shareSharer, referencedName); rec.Code == http.StatusInternalServerError {
			t.Errorf("the sharer needs no version lookup: status = %d", rec.Code)
		}
	})

	t.Run("the share view leaves user-scoped files out and says why", func(t *testing.T) {
		f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
		f.backend.failVersions.Store(true)
		rec := f.viewAs(shareViewer)
		if rec.Code != http.StatusOK {
			t.Fatalf("view: %d", rec.Code)
		}
		if strings.Contains(rec.Body.String(), `"filename":"`+referencedName+`"`) {
			t.Errorf("the view lists a user-scoped file while versions cannot be read: %s", rec.Body.String())
		}
		if got := f.logged("WARN"); len(got) != 1 {
			t.Errorf("warnings = %v, want one", got)
		}
	})

	t.Run("the fork fails before it changes anything", func(t *testing.T) {
		f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))
		before := f.sessionCount(shareViewer)
		f.backend.failVersions.Store(true)

		if rec := f.forkAs(shareViewer); rec.Code != http.StatusInternalServerError {
			t.Errorf("fork: status = %d body = %s, want 500", rec.Code, rec.Body.String())
		}
		if after := f.sessionCount(shareViewer); after != before {
			t.Errorf("the failed fork created a session: %d before, %d after", before, after)
		}
		if got := f.logged("ERROR"); len(got) != 1 {
			t.Errorf("errors = %v, want one", got)
		}

		f.backend.failVersions.Store(false)
		f.backend.failKeys.Store(true)
		if rec := f.forkAs(shareViewer); rec.Code != http.StatusInternalServerError {
			t.Errorf("fork with an unlistable scope: status = %d, want 500", rec.Code)
		}
		if after := f.sessionCount(shareViewer); after != before {
			t.Errorf("the failed fork created a session: %d before, %d after", before, after)
		}
	})
}

func TestShareLinkRefusalLogsOneWarningWithIdsOnly(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))

	f.open(shareViewer, unreferencedName)
	warnings := f.logged("WARN")
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %s", len(warnings), f.logs.text())
	}
	if warnings[0]["shareID"] != f.shareID || warnings[0]["userID"] != shareViewer {
		t.Errorf("warning fields = %v, want the share id and the viewer's id", warnings[0])
	}
	if text := f.logs.text(); strings.Contains(text, unreferencedName) || strings.Contains(text, unreferencedBytes) || strings.Contains(text, "private") {
		t.Errorf("the log names the refused file or its content: %s", text)
	}
}

func TestShareLinkForkLeavesARoutineOmissionOutOfTheWarnings(t *testing.T) {
	f := newShareUserFilesFixture(t, bubblesReferencing(referencedName))

	f.fork(shareViewer)
	if got := f.logged("WARN"); len(got) != 0 {
		t.Errorf("warnings = %v, want none for files a fork routinely leaves out", got)
	}
	debug := f.logged("DEBUG")
	var counted bool
	for _, line := range debug {
		counted = counted || line["count"] == float64(1)
	}
	if !counted {
		t.Errorf("no debug line counts the one file left out: %s", f.logs.text())
	}
	if text := f.logs.text(); strings.Contains(text, unreferencedName) || strings.Contains(text, unreferencedBytes) {
		t.Errorf("the log names the refused file or its content: %s", text)
	}
}

func TestShareArtifactScopeKeepsAStoreFailureForTheHandler(t *testing.T) {
	logs := &warnRecorder{}
	loads := 0
	scope := &shareArtifactScope{
		requester: shareViewer,
		link:      &store.SharedLink{ShareID: "share-1", UserID: shareSharer},
		logger:    logs.logger(),
		load: func() (entrypoint.ReferencedUserFiles, error) {
			loads++
			return nil, errors.New("chat store unavailable")
		},
	}

	for _, name := range []string{referencedName, unreferencedName, absentUserName} {
		if !scope.refuses(t.Context(), name) {
			t.Errorf("%s is admitted although the references could not be loaded", name)
		}
	}
	if scope.refuses(t.Context(), sessionScopedName) {
		t.Error("an ordinary file is refused although it needs no references")
	}
	if scope.unavailable() == nil {
		t.Error("the store failure is not kept for the handler")
	}
	if loads != 1 {
		t.Errorf("references loaded %d times, want once per request", loads)
	}
	if got := len(logs.lines()); got != 0 {
		t.Errorf("the scope logged %d lines for a store failure, want none (the handler logs it once): %s", got, logs.text())
	}
	sharer := &shareArtifactScope{requester: shareSharer, link: scope.link, logger: logs.logger(), load: func() (entrypoint.ReferencedUserFiles, error) {
		t.Error("the sharer's request loaded the references it does not need")
		return nil, nil
	}}
	if sharer.refuses(t.Context(), unreferencedName) {
		t.Error("the sharer is refused a user-scoped file")
	}
}

func TestShareArtifactScopeRefusesAUserScopedNameWhoseVersionsCannotBeListed(t *testing.T) {
	backend := &flakyArtifactBackend{Service: filesystem.New(t.TempDir())}
	backend.failVersions.Store(true)
	scope := &shareArtifactScope{
		requester: shareViewer,
		link:      &store.SharedLink{ShareID: "share-1", UserID: shareSharer, SessionID: "sess-1"},
		artifacts: &artifact.ScopedService{Inner: backend, Scope: "namespace", Namespace: "test-ns"},
		logger:    (&warnRecorder{}).logger(),
		load: func() (entrypoint.ReferencedUserFiles, error) {
			return entrypoint.ReferencedUserFiles{referencedName: 0}, nil
		},
	}

	if !scope.refuses(t.Context(), referencedName) {
		t.Error("a referenced file is admitted although its latest version cannot be read")
	}
	if scope.unavailable() == nil {
		t.Error("the failure is not kept for the handler")
	}
}

func TestRefusesUnreferencedUserScopedName(t *testing.T) {
	vouching := func(names ...string) func(string) bool {
		return func(filename string) bool { return slices.Contains(names, filename) }
	}
	for _, tc := range []struct {
		name                       string
		requester, owner, filename string
		vouched                    func(string) bool
		refused                    bool
	}{
		{"another user, an unvouched user-scoped name", "bob", "alice", unreferencedName, vouching(referencedName), true},
		{"another user, a vouched user-scoped name", "bob", "alice", referencedName, vouching(referencedName), false},
		{"another user, an ordinary name", "bob", "alice", sessionScopedName, vouching(), false},
		{"another user, nothing vouched", "bob", "alice", referencedName, vouching(), true},
		{"the owner, an unvouched user-scoped name", "alice", "alice", unreferencedName, vouching(), false},
		{"the owner in another ASCII case", "ALICE", "alice", unreferencedName, vouching(), false},
		{"Kelvin sign is another user", "Klice", "klice", unreferencedName, vouching(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := refusesUnreferencedUserScopedName(tc.requester, tc.owner, tc.filename, tc.vouched); got != tc.refused {
				t.Errorf("refusesUnreferencedUserScopedName(%q, %q, %q) = %v, want %v", tc.requester, tc.owner, tc.filename, got, tc.refused)
			}
		})
	}
	t.Run("the vouching is not asked about a name that needs none", func(t *testing.T) {
		asked := false
		refusesUnreferencedUserScopedName("alice", "alice", unreferencedName, func(string) bool { asked = true; return false })
		refusesUnreferencedUserScopedName("bob", "alice", sessionScopedName, func(string) bool { asked = true; return false })
		if asked {
			t.Error("the vouching was consulted for the owner or for an ordinary name")
		}
	})
}
