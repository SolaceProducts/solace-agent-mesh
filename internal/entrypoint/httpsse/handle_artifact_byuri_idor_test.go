// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestArtifactByURI_CrossUser_Returns404(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	ctx := context.Background()

	loc := artSvc.Resolve("", "alice", idgen.New(), "alice-secret.txt")
	ver, err := artSvc.Inner.Save(ctx, loc, []byte("alice-only"), "text/plain")
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	uri := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		loc.AppName, loc.UserID, loc.SessionID, loc.Filename, ver)
	req := httptest.NewRequest("GET", "/api/v1/artifacts/byUri?uri="+uri, nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "bob", Email: "bob@example.com"})))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-user by-uri: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) == "alice-only" {
		t.Fatalf("artifact bytes leaked across users")
	}
}

func TestArtifactByURI_OwnerStillWorks(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	ctx := context.Background()

	loc := artSvc.Resolve("", "alice", idgen.New(), "alice-mine.txt")
	ver, err := artSvc.Inner.Save(ctx, loc, []byte("alice-can-see"), "text/plain")
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	uri := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		loc.AppName, loc.UserID, loc.SessionID, loc.Filename, ver)
	req := httptest.NewRequest("GET", "/api/v1/artifacts/byUri?uri="+uri, nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Email: "alice@example.com"})))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("alice GET own artifact via by-uri: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "alice-can-see" {
		t.Errorf("got %q, want %q", string(body), "alice-can-see")
	}
}

func TestArtifactByURI_OwnerCaseInsensitive(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	ctx := context.Background()

	loc := artSvc.Resolve("", "alice@example.com", idgen.New(), "case.txt")
	ver, err := artSvc.Inner.Save(ctx, loc, []byte("yes"), "text/plain")
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	uri := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		loc.AppName, loc.UserID, loc.SessionID, loc.Filename, ver)
	req := httptest.NewRequest("GET", "/api/v1/artifacts/byUri?uri="+uri, nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "Alice@Example.COM", Email: "Alice@Example.COM"})))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("case-insensitive owner check: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestArtifactByURI_CrossUser_NoLeakOfBytes(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	ctx := context.Background()

	const secret = "TOPSECRET-MARKER-DEADBEEF"
	loc := artSvc.Resolve("", "alice", idgen.New(), "secret.txt")
	ver, _ := artSvc.Inner.Save(ctx, loc, []byte(secret), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	uri := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		loc.AppName, loc.UserID, loc.SessionID, loc.Filename, ver)
	req := httptest.NewRequest("GET", "/api/v1/artifacts/byUri?uri="+uri, nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "mallory"})))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), secret) {
		t.Fatalf("response leaked artifact bytes across users; body=%s", string(body))
	}
}
