// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestArtifactByURI_ProjectShare(t *testing.T) {
	const projectBytes = "PROJECT-SPEC-BYTES"
	const aliceSecret = "ALICE-USER-SCOPED-SECRET"
	const carolSecret = "CAROL-FILED-UNDER-THE-PROJECT-ID"

	type fixture struct {
		get     func(user, owner, session, filename string) *httptest.ResponseRecorder
		getCtx  func(ctx context.Context, user, owner, session, filename string) *httptest.ResponseRecorder
		save    func(owner, session, filename, content string)
		project *store.Project
		sharing *mockProjectSharingService
	}
	setup := func(t *testing.T, sharing *mockProjectSharingService) *fixture {
		t.Helper()
		h, _, _ := setupArtifactHandler(t)
		artSvc := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
		h.artSvc = artSvc
		repo := store.NewProjectRepo(h.core.Store())
		h.projectRepo = repo
		project := &store.Project{Name: "shared", UserID: createOwnerAlice}
		if err := repo.Create(t.Context(), project); err != nil {
			t.Fatalf("seed project: %v", err)
		}
		if sharing != nil {
			h.core.SetProjectSharingService(sharing)
		}
		save := func(owner, session, filename, content string) {
			t.Helper()
			if _, err := artSvc.Inner.Save(t.Context(), artSvc.Resolve("", owner, session, filename), []byte(content), "text/plain"); err != nil {
				t.Fatalf("save: %v", err)
			}
		}
		save(createOwnerAlice, projectSessionPrefix+project.ID, "spec.txt", projectBytes)
		save(createOwnerAlice, "ignored-for-a-user-scoped-name", "user:notes.md", aliceSecret)
		save(carolOwner, projectSessionPrefix+project.ID, "spec.txt", carolSecret)

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)
		getCtx := func(ctx context.Context, user, owner, session, filename string) *httptest.ResponseRecorder {
			uri := "artifact://test-ns/" + owner + "/" + session + "/" + filename + "?version=0"
			req := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/byUri?uri="+url.QueryEscape(uri), nil).WithContext(ctx)
			req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: user})))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			return rec
		}
		get := func(user, owner, session, filename string) *httptest.ResponseRecorder {
			return getCtx(t.Context(), user, owner, session, filename)
		}
		return &fixture{get: get, getCtx: getCtx, save: save, project: project, sharing: sharing}
	}

	t.Run("a member of the shared project reads the project's file", func(t *testing.T) {
		f := setup(t, &mockProjectSharingService{hasAccess: true})
		rec := f.get(createOwnerBob, createOwnerAlice, projectSessionPrefix+f.project.ID, "spec.txt")
		if rec.Code != http.StatusOK || rec.Body.String() != projectBytes {
			t.Fatalf("status = %d body = %q, want 200 %q", rec.Code, rec.Body.String(), projectBytes)
		}
		if f.sharing.lastViewAccessUserID != createOwnerBob || f.sharing.lastViewAccessProjectID != f.project.ID {
			t.Errorf("access asked for project %q user %q", f.sharing.lastViewAccessProjectID, f.sharing.lastViewAccessUserID)
		}
	})

	t.Run("someone the project is not shared with gets 404", func(t *testing.T) {
		f := setup(t, &mockProjectSharingService{hasAccess: false})
		if rec := f.get(createOwnerBob, createOwnerAlice, projectSessionPrefix+f.project.ID, "spec.txt"); rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("a failing share lookup is a 500", func(t *testing.T) {
		f := setup(t, &mockProjectSharingService{viewAccessErr: errors.New("sharing backend down")})
		if rec := f.get(createOwnerBob, createOwnerAlice, projectSessionPrefix+f.project.ID, "spec.txt"); rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("a client that has gone away is not answered", func(t *testing.T) {
		f := setup(t, &mockProjectSharingService{viewAccessErr: errors.New("sharing backend down")})
		gone, cancel := context.WithCancel(t.Context())
		cancel()
		rec := f.getCtx(gone, createOwnerBob, createOwnerAlice, projectSessionPrefix+f.project.ID, "spec.txt")
		if rec.Body.Len() != 0 {
			t.Errorf("a client that was gone was answered: %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a share opens nothing but the project's own files", func(t *testing.T) {
		f := setup(t, &mockProjectSharingService{hasAccess: true})
		scope := projectSessionPrefix + f.project.ID
		for _, tc := range []struct {
			name, owner, session, filename, secret string
		}{
			{"another user's files filed under the project id", carolOwner, scope, "spec.txt", carolSecret},
			{"a user-scoped name of the project's owner", createOwnerAlice, scope, "user:notes.md", aliceSecret},
			{"an encoded user-scoped name", createOwnerAlice, scope, "user%3Anotes.md", aliceSecret},
			{"another session of the owner", createOwnerAlice, idgen.New(), "spec.txt", projectBytes},
			{"the owner spelled in another case", "ALICE", scope, "spec.txt", projectBytes},
			{"the project's id without the project- prefix", createOwnerAlice, f.project.ID, "spec.txt", projectBytes},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stored, _ := url.PathUnescape(tc.filename)
				f.save(tc.owner, tc.session, stored, tc.secret)
				rec := f.get(createOwnerBob, tc.owner, tc.session, tc.filename)
				if rec.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 404, body=%q", rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), tc.secret) {
					t.Errorf("bytes leaked: %q", rec.Body.String())
				}
			})
		}
	})

	t.Run("a project id that is not the canonical one gets 404", func(t *testing.T) {
		f := setup(t, &mockProjectSharingService{hasAccess: true})
		for name, session := range map[string]string{
			"upper case":   projectSessionPrefix + strings.ToUpper(f.project.ID),
			"unhyphenated": projectSessionPrefix + strings.ReplaceAll(f.project.ID, "-", ""),
			"not an id":    projectSessionPrefix + "zzz",
			"empty":        projectSessionPrefix,
		} {
			t.Run(name, func(t *testing.T) {
				f.save(createOwnerAlice, session, "spec.txt", projectBytes)
				rec := f.get(createOwnerBob, createOwnerAlice, session, "spec.txt")
				if rec.Code != http.StatusNotFound || rec.Body.String() == projectBytes {
					t.Fatalf("status = %d, want 404 without the bytes", rec.Code)
				}
			})
		}
	})

	t.Run("a project that does not exist gets 404", func(t *testing.T) {
		f := setup(t, &mockProjectSharingService{hasAccess: true})
		if rec := f.get(createOwnerBob, createOwnerAlice, projectSessionPrefix+"0194f4a0-0000-7000-8000-000000000000", "spec.txt"); rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("the project's owner reads the project's file", func(t *testing.T) {
		f := setup(t, nil)
		if rec := f.get(createOwnerAlice, createOwnerAlice, projectSessionPrefix+f.project.ID, "spec.txt"); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}
