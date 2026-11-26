// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func artifactMux(h *artifactHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/artifacts/upload", h.handleUpload)
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)
	mux.HandleFunc("DELETE /api/v1/artifacts/{id}/{filename}", h.handleDelete)
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions", h.handleListVersions)
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions/{version}", h.handleGetVersion)
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)
	return mux
}

func uploadRequest(t *testing.T, sessionID string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("uploadFile", "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("x"))
	_ = w.WriteField("sessionId", sessionID)
	_ = w.WriteField("filename", "x.txt")
	_ = w.Close()
	req := httptest.NewRequest("POST", "/api/v1/artifacts/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

type artifactCase struct {
	name        string
	method      string
	path        string
	bodyBuilder func(t *testing.T) *http.Request
	successCode int
}

func artifactCases(sess string) []artifactCase {
	uploadBody := func(t *testing.T) *http.Request { return uploadRequest(t, sess) }
	return []artifactCase{
		{"upload", "POST", "/api/v1/artifacts/upload", uploadBody, http.StatusCreated},
		{"listAll", "GET", "/api/v1/artifacts/all", nil, http.StatusOK},
		{"list", "GET", "/api/v1/artifacts/" + sess, nil, http.StatusOK},
		{"listVersions", "GET", "/api/v1/artifacts/" + sess + "/x.txt/versions", nil, http.StatusOK},
		{"getLatest", "GET", "/api/v1/artifacts/" + sess + "/x.txt", nil, http.StatusOK},
		{"getVersion", "GET", "/api/v1/artifacts/" + sess + "/x.txt/versions/1", nil, http.StatusOK},
		{"delete", "DELETE", "/api/v1/artifacts/" + sess + "/x.txt", nil, http.StatusNoContent},
	}
}

func dispatchArtifactCase(t *testing.T, mux *http.ServeMux, tc artifactCase, id *auth.Subject) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if tc.bodyBuilder != nil {
		req = tc.bodyBuilder(t)
	} else {
		req = httptest.NewRequest(tc.method, tc.path, nil)
	}
	if id != nil {
		req = req.WithContext(auth.ContextWithSubject(req.Context(), id))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestArtifactEndpoints_NoToolScopes_Work(t *testing.T) {
	h, artSvc, sessRepo := setupArtifactHandler(t)
	h.allCache = newArtifactCache(30*time.Second, 200)
	mux := artifactMux(h)
	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "sam_dev_user",
		Email:         "sam_dev_user",
		Authenticated: true,
		Scopes:        []string{"agent:Orchestrator:invoke"},
	})

	ctx := context.Background()
	sess := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess, UserID: "sam_dev_user"})
	loc := artSvc.Resolve("", "sam_dev_user", sess, "x.txt")
	if _, err := artSvc.Inner.Save(ctx, loc, []byte("hello"), "text/plain"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range artifactCases(sess) {
		t.Run(tc.name, func(t *testing.T) {
			rec := dispatchArtifactCase(t, mux, tc, id)

			if rec.Code == http.StatusForbidden {
				t.Fatalf("expected non-403 without artifact scopes, got 403; body: %s", rec.Body.String())
			}
			if rec.Code != tc.successCode {
				t.Errorf("expected %d, got %d; body: %s", tc.successCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestArtifactRoutesCarryNoScopeGate(t *testing.T) {
	adapter := newCoverageAdapter(t)
	stateChanging := map[string]bool{"POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	isArtifactFamily := func(path string) bool {
		return strings.HasPrefix(path, "/api/v1/artifacts/") ||
			path == "/api/v1/sessions/{id}/artifacts/copy"
	}

	seen := 0
	for _, r := range adapter.Routes() {
		if !isArtifactFamily(r.Path) {
			continue
		}
		seen++
		if r.Scope != "" {
			t.Errorf("route %s %s carries Scope=%q — artifact routes are ownership-gated by design, not scope-gated", r.Method, r.Path, r.Scope)
		}
		if stateChanging[r.Method] && r.IsPublic != publicReasonOwnerScoped {
			t.Errorf("state-changing route %s %s must carry publicReasonOwnerScoped, got IsPublic=%q", r.Method, r.Path, r.IsPublic)
		}
	}
	if seen < 10 {
		t.Fatalf("expected at least 10 artifact-family routes in the registry, saw %d — path matching is stale", seen)
	}
}
