// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var foldedIdentities = []struct{ name, caller, owner string }{
	{"dotted capital I", "İvan@x.com", "ivan@x.com"},
	{"Kelvin sign", "Kate@x.com", "kate@x.com"},
	{"long s", "ſam@x.com", "sam@x.com"},
	{"dotless i", "ıvan@x.com", "ivan@x.com"},
	{"dotless i against capital I", "ıvan@x.com", "Ivan@x.com"},
}

func TestHandleMessage_ProjectContext_FoldedIdentitiesAreOtherUsers(t *testing.T) {
	const marker = "PROJECT-INSTRUCTIONS-MARKER"
	for _, tc := range foldedIdentities {
		t.Run(tc.name, func(t *testing.T) {
			f, q := newMessageOwnerFixture(t)
			repo := store.NewProjectRepo(f.core.Store())
			f.handler.projectRepo = repo
			prompt := marker
			project := &store.Project{Name: "mine", UserID: tc.owner, SystemPrompt: &prompt}
			if err := repo.Create(t.Context(), project); err != nil {
				t.Fatalf("seed project: %v", err)
			}
			f.core.SetProjectSharingService(&mockProjectSharingService{hasAccess: false})
			metadata := map[string]any{"project_id": project.ID}

			if rec := f.postCtx(t.Context(), messageStreamRoute, tc.caller, "", metadata); rec.Code != http.StatusOK {
				t.Fatalf("caller: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
			}
			if msg := publishedTask(t, f.brk, q, 2*time.Second); msg == nil || strings.Contains(string(msg.Payload), marker) {
				t.Errorf("another user's project Instructions reached the first message: %v", msg)
			}

			if rec := f.postCtx(t.Context(), messageStreamRoute, tc.owner, "", metadata); rec.Code != http.StatusOK {
				t.Fatalf("owner: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
			}
			if msg := publishedTask(t, f.brk, q, 2*time.Second); msg == nil || !strings.Contains(string(msg.Payload), marker) {
				t.Errorf("the owner's project Instructions did not reach the first message: %v", msg)
			}
		})
	}
}

func TestAccessibleProject_FoldedIdentitiesAreOtherUsers(t *testing.T) {
	for _, tc := range foldedIdentities {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := newMessageOwnerFixture(t)
			project := f.withProjects(t, tc.owner)
			f.core.SetProjectSharingService(&mockProjectSharingService{hasAccess: false})

			if got, err := f.handler.accessibleProject(t.Context(), project.ID, tc.caller); err != nil || got != nil {
				t.Errorf("accessibleProject for %q on %q's project = %+v, %v; want nil", tc.caller, tc.owner, got, err)
			}
			if got, err := f.handler.accessibleProject(t.Context(), project.ID, tc.owner); err != nil || got == nil {
				t.Errorf("accessibleProject for the owner %q = %+v, %v; want the project", tc.owner, got, err)
			}
		})
	}
}

func TestHandleMessage_ArtifactReference_FoldedIdentitiesAreOtherUsers(t *testing.T) {
	for _, tc := range foldedIdentities {
		t.Run(tc.name, func(t *testing.T) {
			f, q := newMessageOwnerFixture(t)
			project := f.withProjects(t, tc.owner)
			f.core.SetProjectSharingService(&mockProjectSharingService{hasAccess: false})
			for _, uri := range []string{
				artifactURI(tc.owner, idgen.New(), "spec.txt"),
				artifactURI(tc.owner, projectSessionPrefix+project.ID, "spec.txt"),
			} {
				trackedBefore := f.sse.totalTasks()
				assertArtifactRefused(t, f, q, f.postParts(messageStreamRoute, tc.caller, filePart(uri)), trackedBefore)
			}
		})
	}
}

func TestArtifactProjectScopeRead_FoldedIdentitiesAreOtherUsers(t *testing.T) {
	const secret = "OWNER-ONLY-BYTES"
	for _, tc := range foldedIdentities {
		t.Run(tc.name, func(t *testing.T) {
			h, artSvc, _ := setupArtifactHandler(t)
			repo := store.NewProjectRepo(h.core.Store())
			h.projectRepo = repo
			h.core.SetProjectSharingService(&mockProjectSharingService{hasAccess: false})
			project := &store.Project{Name: "mine", UserID: tc.owner}
			if err := repo.Create(t.Context(), project); err != nil {
				t.Fatalf("seed project: %v", err)
			}
			if _, err := artSvc.Inner.Save(t.Context(), artSvc.Resolve("", tc.owner, projectSessionPrefix+project.ID, "spec.txt"), []byte(secret), "text/plain"); err != nil {
				t.Fatalf("save: %v", err)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/x/spec.txt?projectId="+project.ID, nil)
			req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: tc.caller})))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), secret) {
				t.Errorf("status = %d body = %q, want 403 without the bytes", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestArtifactByURI_FoldedIdentitiesAreOtherUsers(t *testing.T) {
	const secret = "OWNER-ONLY-BYTES"
	for _, tc := range foldedIdentities {
		t.Run(tc.name, func(t *testing.T) {
			h, artSvc, _ := setupArtifactHandler(t)
			repo := store.NewProjectRepo(h.core.Store())
			h.projectRepo = repo
			h.core.SetProjectSharingService(&mockProjectSharingService{hasAccess: false})
			project := &store.Project{Name: "mine", UserID: tc.owner}
			if err := repo.Create(t.Context(), project); err != nil {
				t.Fatalf("seed project: %v", err)
			}
			sessions := []string{idgen.New(), projectSessionPrefix + project.ID}
			for _, session := range sessions {
				if _, err := artSvc.Inner.Save(t.Context(), artSvc.Resolve("", tc.owner, session, "spec.txt"), []byte(secret), "text/plain"); err != nil {
					t.Fatalf("save: %v", err)
				}
			}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)
			for _, session := range sessions {
				uri := "artifact://test-ns/" + tc.owner + "/" + session + "/spec.txt?version=0"
				req := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/byUri?uri="+url.QueryEscape(uri), nil)
				req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: tc.caller})))
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), secret) {
					t.Errorf("session %q: status = %d body = %q, want 404 without the bytes", session, rec.Code, rec.Body.String())
				}
			}
		})
	}
}
