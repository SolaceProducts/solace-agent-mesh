// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	userScopedName   = "user:notes.md"
	userScopedSecret = "OWNER-USER-SCOPED-SECRET"
	projectFileName  = "spec.txt"
	projectFileBytes = "PROJECT-SPEC-BYTES"
	otherSessionID   = "an-unrelated-session"
)

func TestRefusesForeignUserScopedName(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		requester, owner, filename string
		refused                    bool
	}{
		{"another user, user-scoped name", "bob", "alice", "user:notes.md", true},
		{"another user, a name that only contains the prefix", "bob", "alice", "notes-user:md", false},
		{"another user, a name that has the prefix in another case", "bob", "alice", "User:notes.md", false},
		{"another user, the bare prefix", "bob", "alice", "user:", true},
		{"another user, an ordinary name", "bob", "alice", "spec.txt", false},
		{"the owner", "alice", "alice", "user:notes.md", false},
		{"the owner spelled in another ASCII case", "ALICE", "alice", "user:notes.md", false},
		{"dotted capital I is another user", "\u0130van@x.com", "ivan@x.com", "user:notes.md", true},
		{"Kelvin sign is another user", "\u212Aate@x.com", "kate@x.com", "user:notes.md", true},
		{"long s is another user", "\u017Fam@x.com", "sam@x.com", "user:notes.md", true},
		{"dotless i is another user", "\u0131van@x.com", "ivan@x.com", "user:notes.md", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := refusesForeignUserScopedName(tc.requester, tc.owner, tc.filename); got != tc.refused {
				t.Errorf("refusesForeignUserScopedName(%q, %q, %q) = %v, want %v", tc.requester, tc.owner, tc.filename, got, tc.refused)
			}
		})
	}
}

func TestAdmittedInfos(t *testing.T) {
	infos := []ArtifactInfoResponse{{Filename: "spec.txt"}, {Filename: userScopedName}}
	if got := admittedInfos(infos, "bob", "alice"); len(got) != 1 || got[0].Filename != "spec.txt" {
		t.Errorf("for another user: %+v, want only spec.txt", got)
	}
	if got := admittedInfos(infos, "alice", "alice"); len(got) != 2 {
		t.Errorf("for the owner: %+v, want both", got)
	}
	if got := admittedInfos([]ArtifactInfoResponse{{Filename: userScopedName}}, "bob", "alice"); got == nil || len(got) != 0 {
		t.Errorf("nothing admitted = %#v, want an empty, non-nil list", got)
	}
}

type warnRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *warnRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *warnRecorder) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (w *warnRecorder) lines() []map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []map[string]any
	for line := range bytes.SplitSeq(w.buf.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

func (w *warnRecorder) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *warnRecorder) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Reset()
}

type projectReadFixture struct {
	h       *artifactHandler
	artSvc  *artifact.ScopedService
	project *store.Project
	owner   string
	logs    *warnRecorder
	mux     *http.ServeMux
}

func newProjectReadFixture(t *testing.T, owner string) *projectReadFixture {
	t.Helper()
	h, _, _ := setupArtifactHandler(t)
	artSvc := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	h.artSvc = artSvc
	h.allCache = newArtifactCache(30*time.Second, 200)
	h.projectRepo = store.NewProjectRepo(h.core.Store())
	logs := &warnRecorder{}
	h.logger = logs.logger()

	project := &store.Project{Name: "shared", UserID: owner}
	if err := h.projectRepo.Create(t.Context(), project); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	h.core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true, sharedIDs: []string{project.ID}})

	f := &projectReadFixture{h: h, artSvc: artSvc, project: project, owner: owner, logs: logs}
	f.save(t, owner, projectSessionPrefix+project.ID, projectFileName, projectFileBytes)
	f.save(t, owner, otherSessionID, userScopedName, userScopedSecret)
	f.save(t, owner, otherSessionID, userScopedName, userScopedSecret)

	f.mux = http.NewServeMux()
	f.mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)
	f.mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)
	f.mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions/{version}", h.handleGetVersion)
	f.mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions", h.handleListVersions)
	f.mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)
	return f
}

func (f *projectReadFixture) save(t *testing.T, owner, session, filename, content string) {
	t.Helper()
	loc := f.artSvc.Resolve("", owner, session, filename)
	if _, err := f.artSvc.Inner.Save(context.Background(), loc, []byte(content), "text/plain"); err != nil {
		panic("seed artifact: " + err.Error())
	}
}

func (f *projectReadFixture) get(user, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: user})))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *projectReadFixture) projectQuery() string { return "?projectId=" + f.project.ID }

func (f *projectReadFixture) refusalWarnings() []map[string]any {
	var out []map[string]any
	for _, rec := range f.logs.lines() {
		if rec["level"] == "WARN" && strings.Contains(rec["msg"].(string), "artifact read refused") {
			out = append(out, rec)
		}
	}
	return out
}

func refusalMessage(rec *httptest.ResponseRecorder) string {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return rec.Body.String()
	}
	return body.Message
}

func listedNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []struct {
			Filename string `json:"filename"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if body.Data == nil {
		t.Fatalf("data is null, want an array: %s", rec.Body.String())
	}
	names := make([]string, 0, len(body.Data))
	for _, d := range body.Data {
		names = append(names, d.Filename)
	}
	slices.Sort(names)
	return names
}

func TestProjectScopeRead_UserScopedNameIsRefusedForAMember(t *testing.T) {
	const member = createOwnerBob
	f := newProjectReadFixture(t, createOwnerAlice)
	encoded := "user%3Anotes.md"

	routes := map[string]string{
		"latest":            "/api/v1/artifacts/x/" + encoded + f.projectQuery(),
		"latest, raw colon": "/api/v1/artifacts/x/" + userScopedName + f.projectQuery(),
		"preview":           "/api/v1/artifacts/x/" + encoded + f.projectQuery() + "&maxBytes=8",
		"versions":          "/api/v1/artifacts/x/" + encoded + "/versions" + f.projectQuery(),
		"version 0":         "/api/v1/artifacts/x/" + encoded + "/versions/0" + f.projectQuery(),
		"version 1":         "/api/v1/artifacts/x/" + encoded + "/versions/1" + f.projectQuery(),
		"version latest":    "/api/v1/artifacts/x/" + encoded + "/versions/latest" + f.projectQuery(),
	}
	for name, target := range routes {
		t.Run(name, func(t *testing.T) {
			f.logs.reset()
			rec := f.get(member, target)
			if rec.Code != http.StatusNotFound || refusalMessage(rec) != artifactNotFoundMsg {
				t.Fatalf("status = %d body = %q, want 404 %q", rec.Code, rec.Body.String(), artifactNotFoundMsg)
			}
			if strings.Contains(rec.Body.String(), userScopedSecret) {
				t.Errorf("bytes leaked: %q", rec.Body.String())
			}
			warnings := f.refusalWarnings()
			if len(warnings) != 1 {
				t.Fatalf("%d refusal warnings, want 1: %v", len(warnings), f.logs.lines())
			}
			if warnings[0]["userID"] != member || warnings[0]["projectID"] != f.project.ID {
				t.Errorf("warning fields = %v, want the caller and the project ids", warnings[0])
			}
			raw := f.logs.text()
			if strings.Contains(raw, "notes") || strings.Contains(raw, userScopedSecret) {
				t.Errorf("the refusal log carries the file name or its bytes: %s", raw)
			}
		})
	}

	t.Run("the answer is the same for a name that does not exist, on every route", func(t *testing.T) {
		for _, suffix := range []string{"", "/versions", "/versions/0", "/versions/1", "/versions/latest"} {
			ghost := f.get(member, "/api/v1/artifacts/x/user%3Aghost.md"+suffix+f.projectQuery())
			existing := f.get(member, "/api/v1/artifacts/x/"+encoded+suffix+f.projectQuery())
			if ghost.Code != http.StatusNotFound || ghost.Code != existing.Code || refusalMessage(ghost) != refusalMessage(existing) {
				t.Errorf("route %q: a missing user-scoped name answers %d %q, an existing one %d %q",
					suffix, ghost.Code, refusalMessage(ghost), existing.Code, refusalMessage(existing))
			}
		}
	})

	t.Run("the refusal reads like the store's own not-found answer", func(t *testing.T) {
		for _, suffix := range []string{"", "/versions/0", "/versions/1", "/versions/latest"} {
			missing := f.get(member, "/api/v1/artifacts/x/no-such-file.txt"+suffix+f.projectQuery())
			refused := f.get(member, "/api/v1/artifacts/x/"+encoded+suffix+f.projectQuery())
			if missing.Code != refused.Code || refusalMessage(missing) != refusalMessage(refused) {
				t.Errorf("route %q: a missing name answers %d %q, the refusal %d %q",
					suffix, missing.Code, refusalMessage(missing), refused.Code, refusalMessage(refused))
			}
		}
	})
}

func TestProjectScopeRead_ProjectFilesStayReadableByAMember(t *testing.T) {
	f := newProjectReadFixture(t, createOwnerAlice)
	for name, target := range map[string]string{
		"latest":         "/api/v1/artifacts/x/" + projectFileName + f.projectQuery(),
		"version 0":      "/api/v1/artifacts/x/" + projectFileName + "/versions/0" + f.projectQuery(),
		"version latest": "/api/v1/artifacts/x/" + projectFileName + "/versions/latest" + f.projectQuery(),
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.get(createOwnerBob, target)
			if rec.Code != http.StatusOK || rec.Body.String() != projectFileBytes {
				t.Fatalf("status = %d body = %q, want 200 %q", rec.Code, rec.Body.String(), projectFileBytes)
			}
		})
	}
	rec := f.get(createOwnerBob, "/api/v1/artifacts/x/"+projectFileName+"/versions"+f.projectQuery())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "[0]") {
		t.Errorf("versions: status = %d body = %q, want 200 with version 0", rec.Code, rec.Body.String())
	}
	if warnings := f.refusalWarnings(); len(warnings) != 0 {
		t.Errorf("a project file read logged a refusal: %v", warnings)
	}
}

func TestProjectScopeRead_TheOwnerKeepsHerUserScopedFiles(t *testing.T) {
	f := newProjectReadFixture(t, createOwnerAlice)
	for name, target := range map[string]string{
		"latest through the project":    "/api/v1/artifacts/x/user%3Anotes.md" + f.projectQuery(),
		"version 0 through the project": "/api/v1/artifacts/x/user%3Anotes.md/versions/0" + f.projectQuery(),
		"latest through a session":      "/api/v1/artifacts/" + otherSessionID + "/user%3Anotes.md",
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.get(createOwnerAlice, target)
			if rec.Code != http.StatusOK || rec.Body.String() != userScopedSecret {
				t.Fatalf("status = %d body = %q, want 200 %q", rec.Code, rec.Body.String(), userScopedSecret)
			}
		})
	}
	if names := listedNames(t, f.get(createOwnerAlice, "/api/v1/artifacts/x"+f.projectQuery())); !slices.Equal(names, []string{projectFileName, userScopedName}) {
		t.Errorf("the owner's project listing = %v, want both files", names)
	}
	if warnings := f.refusalWarnings(); len(warnings) != 0 {
		t.Errorf("an owner read logged a refusal: %v", warnings)
	}
}

func TestProjectScopeRead_FoldedIdentitiesAreOtherUsers(t *testing.T) {
	for _, tc := range foldedIdentities {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectReadFixture(t, tc.owner)
			rec := f.get(tc.caller, "/api/v1/artifacts/x/user%3Anotes.md"+f.projectQuery())
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), userScopedSecret) {
				t.Errorf("read: status = %d body = %q, want 404 without the bytes", rec.Code, rec.Body.String())
			}
			if names := listedNames(t, f.get(tc.caller, "/api/v1/artifacts/x"+f.projectQuery())); !slices.Equal(names, []string{projectFileName}) {
				t.Errorf("listing = %v, want only the project's own file", names)
			}
			if rec = f.get(tc.owner, "/api/v1/artifacts/x/user%3Anotes.md"+f.projectQuery()); rec.Code != http.StatusOK {
				t.Errorf("the owner: status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestProjectScopeList_LeavesOutUserScopedFilesOfAnotherUser(t *testing.T) {
	f := newProjectReadFixture(t, createOwnerAlice)
	for _, visibility := range []string{"", "&visibility=all"} {
		names := listedNames(t, f.get(createOwnerBob, "/api/v1/artifacts/x"+f.projectQuery()+visibility))
		if !slices.Equal(names, []string{projectFileName}) {
			t.Errorf("member listing%s = %v, want only the project's own file", visibility, names)
		}
	}

	t.Run("a project holding only a user-scoped file lists as an empty array", func(t *testing.T) {
		only := newProjectReadFixture(t, createOwnerAlice)
		if err := only.artSvc.Inner.Delete(t.Context(), only.artSvc.Resolve("", createOwnerAlice, projectSessionPrefix+only.project.ID, projectFileName)); err != nil {
			t.Fatalf("delete project file: %v", err)
		}
		if names := listedNames(t, only.get(createOwnerBob, "/api/v1/artifacts/x"+only.projectQuery())); len(names) != 0 {
			t.Errorf("listing = %v, want empty", names)
		}
	})
}

func TestListAll_ProjectJobsLeaveOutUserScopedFilesOfAnotherUser(t *testing.T) {
	f := newProjectReadFixture(t, createOwnerAlice)

	member := listAllNames(t, f, createOwnerBob, "")
	if !slices.Equal(member, []string{projectFileName}) {
		t.Errorf("member /artifacts/all = %v, want only the project's own file", member)
	}
	if got := listAllNames(t, f, createOwnerBob, f.projectQuery()[1:]); !slices.Equal(got, []string{projectFileName}) {
		t.Errorf("member /artifacts/all for the project = %v, want only the project's own file", got)
	}

	f.h.allCache.invalidate(createOwnerAlice)
	if owner := listAllNames(t, f, createOwnerAlice, ""); !slices.Equal(owner, []string{projectFileName, userScopedName}) {
		t.Errorf("owner /artifacts/all = %v, want both files", owner)
	}
}

func listAllNames(t *testing.T, f *projectReadFixture, user, query string) []string {
	t.Helper()
	target := "/api/v1/artifacts/all?pageSize=100"
	if query != "" {
		target += "&" + query
	}
	return listedNames(t, f.get(user, target))
}

func TestProjectListing_LeavesOutAndDoesNotCountUserScopedFilesOfAnotherUser(t *testing.T) {
	f := newProjectReadFixture(t, createOwnerAlice)
	ph := &projectHandler{core: f.h.core, projectRepo: f.h.projectRepo, artSvc: f.artSvc, logger: f.h.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/artifacts", ph.handleListArtifacts)
	mux.HandleFunc("GET /api/v1/projects/{id}", ph.handleGet)
	mux.HandleFunc("GET /api/v1/projects", ph.handleList)
	serve := func(user, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: user})))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	artifactCount := func(t *testing.T, rec *httptest.ResponseRecorder, inList bool) int {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		var one ProjectResponse
		if !inList {
			if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil || one.ArtifactCount == nil {
				t.Fatalf("decode project: %v %s", err, rec.Body.String())
			}
			return *one.ArtifactCount
		}
		var list struct {
			Data []ProjectResponse `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode projects: %v", err)
		}
		for _, p := range list.Data {
			if p.ID == f.project.ID && p.ArtifactCount != nil {
				return *p.ArtifactCount
			}
		}
		t.Fatalf("project %s with an artifact count missing from %s", f.project.ID, rec.Body.String())
		return 0
	}
	listURL := "/api/v1/projects/" + f.project.ID + "/artifacts"
	getURL := "/api/v1/projects/" + f.project.ID
	const listAllURL = "/api/v1/projects?expand=artifactCount"

	t.Run("a member", func(t *testing.T) {
		if names := listedNames(t, serve(createOwnerBob, listURL)); !slices.Equal(names, []string{projectFileName}) {
			t.Errorf("project artifacts = %v, want only the project's own file", names)
		}
		if got := artifactCount(t, serve(createOwnerBob, getURL), false); got != 1 {
			t.Errorf("project artifactCount = %d, want 1", got)
		}
		if got := artifactCount(t, serve(createOwnerBob, listAllURL), true); got != 1 {
			t.Errorf("project list artifactCount = %d, want 1", got)
		}
	})

	t.Run("the owner", func(t *testing.T) {
		if names := listedNames(t, serve(createOwnerAlice, listURL)); !slices.Equal(names, []string{projectFileName, userScopedName}) {
			t.Errorf("project artifacts = %v, want both files", names)
		}
		if got := artifactCount(t, serve(createOwnerAlice, getURL), false); got != 2 {
			t.Errorf("project artifactCount = %d, want 2", got)
		}
		if got := artifactCount(t, serve(createOwnerAlice, listAllURL), true); got != 2 {
			t.Errorf("project list artifactCount = %d, want 2", got)
		}
	})
}

func TestCopyProjectArtifactsToSession_LeavesOutUserScopedFilesOfAnotherUser(t *testing.T) {
	f := newProjectReadFixture(t, createOwnerAlice)
	logger := slog.New(slog.DiscardHandler)
	loadUserScoped := func(user string) []byte {
		content, _, err := f.artSvc.Inner.Load(t.Context(), f.artSvc.Resolve("", user, idgen.New(), userScopedName), nil)
		if err != nil {
			t.Fatalf("load %s's user-scoped file: %v", user, err)
		}
		return content
	}

	t.Run("a member gets the project's files and none of the owner's user-scoped ones", func(t *testing.T) {
		session := idgen.New()
		copied, names, err := copyProjectArtifactsToSession(t.Context(), f.artSvc, f.project.ID, createOwnerAlice, createOwnerBob, session, true, true, logger)
		if err != nil {
			t.Fatalf("copy: %v", err)
		}
		if copied != 1 || !slices.Equal(names, []string{projectFileName}) {
			t.Errorf("copied %d %v, want only %s", copied, names, projectFileName)
		}
		if content := loadUserScoped(createOwnerBob); content != nil {
			t.Errorf("the owner's user-scoped file was copied into the member's user scope: %q", content)
		}
	})

	t.Run("the owner's copy is unchanged", func(t *testing.T) {
		session := idgen.New()
		_, names, err := copyProjectArtifactsToSession(t.Context(), f.artSvc, f.project.ID, createOwnerAlice, createOwnerAlice, session, true, true, logger)
		if err != nil {
			t.Fatalf("copy: %v", err)
		}
		if !slices.Equal(names, []string{projectFileName, userScopedName}) {
			t.Errorf("copied %v, want both files", names)
		}
	})
}
