// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type v2Artifacts struct {
	t      *testing.T
	h      *artifactHandler
	svc    *artifact.ScopedService
	sess   *store.SessionRepo
	mux    *http.ServeMux
	routes []apicontract.Route
}

func newV2Artifacts(t *testing.T) *v2Artifacts {
	t.Helper()
	h, svc, sess := setupArtifactHandler(t)
	routes := h.chatV2Routes()
	mux := http.NewServeMux()
	apicontract.Mount(mux, chatv2.Prefix, routes, chatV2Guard, nil)
	return &v2Artifacts{t: t, h: h, svc: svc, sess: sess, mux: mux, routes: routes}
}

func (f *v2Artifacts) call(method, path, caller string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := withIdentity(httptest.NewRequest(method, chatv2.Prefix+path, nil), caller)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *v2Artifacts) store(owner, filename, content string) (sessionID string, version int) {
	f.t.Helper()
	sessionID = idgen.New()
	if err := f.sess.Create(context.Background(), &store.Session{ID: sessionID, UserID: owner}); err != nil {
		f.t.Fatal(err)
	}
	loc := f.svc.Resolve("", owner, sessionID, filename)
	version, err := f.svc.Inner.Save(context.Background(), loc, []byte(content), "text/plain")
	if err != nil {
		f.t.Fatal(err)
	}
	return sessionID, version
}

func TestChatV2ArtifactDownload_OwnerGetsTheBytesAsAnAttachment(t *testing.T) {
	f := newV2Artifacts(t)
	session, _ := f.store(v2Alice, "notes.txt", "alice's notes")

	rec := f.call(http.MethodGet, "/sessions/"+session+"/artifacts/notes.txt", v2Alice)

	if rec.Code != http.StatusOK || rec.Body.String() != "alice's notes" {
		t.Fatalf("download = %d %q, want 200 with the bytes", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Disposition"); got == "" || got[:10] != "attachment" {
		t.Errorf("Content-Disposition = %q, want an attachment", got)
	}
}

func TestChatV2ArtifactDownload_AnotherUserGets404ByNameAndByURI(t *testing.T) {
	f := newV2Artifacts(t)
	session, version := f.store(v2Alice, "secret.txt", "alice only")
	v1 := f.call(http.MethodGet, "/sessions/"+session+"/artifacts/secret.txt", v2Bob)
	loc := f.svc.Resolve("", v2Alice, session, "secret.txt")
	uri := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d", loc.AppName, loc.UserID, loc.SessionID, loc.Filename, version)

	byURI := f.call(http.MethodGet, "/artifacts/byUri?uri="+url.QueryEscape(uri), v2Bob)

	for name, rec := range map[string]*httptest.ResponseRecorder{"by name": v1, "by uri": byURI} {
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: another user's download = %d %s, want 404", name, rec.Code, rec.Body)
		}
		if rec.Body.String() == "alice only" {
			t.Errorf("%s: the bytes leaked", name)
		}
	}
}

func TestChatV2ArtifactDownload_VersionLatestAndNumbered(t *testing.T) {
	f := newV2Artifacts(t)
	session, version := f.store(v2Alice, "notes.txt", "v0")
	base := "/sessions/" + session + "/artifacts/notes.txt/versions/"

	for _, v := range []string{"latest", fmt.Sprint(version)} {
		if rec := f.call(http.MethodGet, base+v, v2Alice); rec.Code != http.StatusOK || rec.Body.String() != "v0" {
			t.Errorf("version %s = %d %q, want 200 v0", v, rec.Code, rec.Body)
		}
	}
	if rec := f.call(http.MethodGet, base+"abc", v2Alice); rec.Code != http.StatusBadRequest {
		t.Errorf("version abc = %d, want 400", rec.Code)
	}
	if rec := f.call(http.MethodGet, base+"-1", v2Alice); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("version -1 = %d, want 422", rec.Code)
	}
}

func TestChatV2ArtifactDownload_MaxBytesCutsAPreviewAndSaysSo(t *testing.T) {
	f := newV2Artifacts(t)
	session, _ := f.store(v2Alice, "long.txt", "0123456789")

	rec := f.call(http.MethodGet, "/sessions/"+session+"/artifacts/long.txt?maxBytes=4", v2Alice)

	if rec.Body.String() != "0123" || rec.Header().Get("X-Truncated") != "true" || rec.Header().Get("X-Original-Size") != "10" {
		t.Errorf("preview = %q truncated=%q size=%q", rec.Body, rec.Header().Get("X-Truncated"), rec.Header().Get("X-Original-Size"))
	}
	if rec := f.call(http.MethodGet, "/sessions/"+session+"/artifacts/long.txt?maxBytes=0x", v2Alice); rec.Code != http.StatusBadRequest {
		t.Errorf("a malformed maxBytes = %d, want 400", rec.Code)
	}
	if rec := f.call(http.MethodGet, "/sessions/"+session+"/artifacts/long.txt?maxBytes=0", v2Alice); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("an out of range maxBytes = %d, want 422", rec.Code)
	}
}

func TestChatV2ArtifactDownload_MissingFileIs404(t *testing.T) {
	f := newV2Artifacts(t)
	session, _ := f.store(v2Alice, "notes.txt", "x")
	if rec := f.call(http.MethodGet, "/sessions/"+session+"/artifacts/nothing.txt", v2Alice); rec.Code != http.StatusNotFound {
		t.Errorf("missing file = %d, want 404", rec.Code)
	}
}

func TestChatV2ArtifactDownload_ThroughAProjectFollowsViewAccess(t *testing.T) {
	tests := map[string]struct {
		reader    string
		hasAccess bool
		want      int
	}{
		"owner":               {v2Alice, false, http.StatusOK},
		"shared viewer":       {v2Bob, true, http.StatusOK},
		"unshared other user": {v2Bob, false, http.StatusNotFound},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f := newV2Artifacts(t)
			f.h.projectRepo = store.NewProjectRepo(f.h.core.Store())
			f.h.core.SetProjectSharingService(&mockProjectSharingService{hasAccess: tt.hasAccess})
			project := &store.Project{Name: "Runbooks", UserID: v2Alice}
			if err := f.h.projectRepo.Create(context.Background(), project); err != nil {
				t.Fatal(err)
			}
			loc := f.svc.Resolve("", v2Alice, projectSessionPrefix+project.ID, "spec.md")
			if _, err := f.svc.Inner.Save(context.Background(), loc, []byte("spec"), "text/plain"); err != nil {
				t.Fatal(err)
			}

			rec := f.call(http.MethodGet, "/sessions/"+idgen.New()+"/artifacts/spec.md?projectId="+project.ID, tt.reader)

			if rec.Code != tt.want {
				t.Errorf("%s: status = %d %s, want %d", name, rec.Code, rec.Body, tt.want)
			}
		})
	}
}
