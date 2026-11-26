// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestSPAHandler_ServesIndex(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("<!DOCTYPE html><html></html>")},
		"assets/app.js": {Data: []byte("// js")},
	}
	h := SPAHandler(fsys)

	for _, tc := range []struct {
		path    string
		wantSub string
	}{
		{"/", "<!DOCTYPE html>"},
		{"/index.html", "<!DOCTYPE html>"},
		{"/some-page", "<!DOCTYPE html>"},
		{"/deep/nested/route", "<!DOCTYPE html>"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", tc.path, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, tc.wantSub) {
			t.Errorf("GET %s: body = %q, want index.html", tc.path, body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: Content-Type = %q, want text/html", tc.path, ct)
		}
	}
}

func TestSPAHandler_ServesStaticAssets(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("<!DOCTYPE html>")},
		"assets/app.js": {Data: []byte("// js")},
	}
	h := SPAHandler(fsys)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /assets/app.js: got %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "// js" {
		t.Errorf("GET /assets/app.js: body = %q, want the asset", body)
	}
}

func TestSPAHandler_DocumentsAreNeverStored(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":               {Data: []byte("<!DOCTYPE html><html>main</html>")},
		"auth-callback.html":       {Data: []byte("<!DOCTYPE html><html>callback</html>")},
		"ui-version.json":          {Data: []byte(`{"version":"sam"}`)},
		"assets/main-AbCd1234.js":  {Data: []byte("// hashed")},
		"assets/notes-for-ops.txt": {Data: []byte("not hashed")},
	}
	h := SPAHandler(fsys)

	for _, tc := range []struct{ method, path, wantBody string }{
		{http.MethodGet, "/", "main"},
		{http.MethodGet, "/index.html", "main"},
		{http.MethodGet, "/auth-callback.html", "callback"},
		{http.MethodGet, "/ui-version.json", `"sam"`},
		{http.MethodGet, "/deep/nested/route", "main"},
		{http.MethodGet, "/assets/notes-for-ops.txt", "not hashed"},
		{http.MethodHead, "/", ""},
		{http.MethodHead, "/auth-callback.html", ""},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s %s: got %d, want 200", tc.method, tc.path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s %s: Cache-Control = %q, want %q", tc.method, tc.path, got, "no-store")
		}
		if got := rec.Header().Get("Last-Modified"); got != "" {
			t.Errorf("%s %s: Last-Modified = %q, want none so nothing can revalidate by timestamp", tc.method, tc.path, got)
		}
		if body := rec.Body.String(); !strings.Contains(body, tc.wantBody) {
			t.Errorf("%s %s: body = %q, want it to contain %q", tc.method, tc.path, body, tc.wantBody)
		}
	}
}

func TestSPAHandler_HashedAssetsAreImmutable(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":                    {Data: []byte("<!DOCTYPE html>")},
		"assets/main-AbCd1234.js":       {Data: []byte("// js")},
		"assets/main-AbCd1234.js.map":   {Data: []byte("{}")},
		"assets/main-Zy_x-987.css":      {Data: []byte("body{}")},
		"assets/ooxml_bg-0aBcDeFg.wasm": {Data: []byte("\x00asm")},
		"assets/favicon-BLgzUch9.ico":   {Data: []byte("ico")},
		"assets/main-ABC.js":            {Data: []byte("// short hash")},
		"assets/readme.txt":             {Data: []byte("plain")},
	}
	h := SPAHandler(fsys)

	for _, path := range []string{
		"/assets/main-AbCd1234.js",
		"/assets/main-AbCd1234.js.map",
		"/assets/main-Zy_x-987.css",
		"/assets/ooxml_bg-0aBcDeFg.wasm",
		"/assets/favicon-BLgzUch9.ico",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("GET %s: Cache-Control = %q, want immutable", path, got)
		}
	}

	for _, path := range []string{"/assets/main-ABC.js", "/assets/readme.txt"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s: Cache-Control = %q, want %q for a file whose name does not prove its content", path, got, "no-store")
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /assets/: got %d, want 404", rec.Code)
	}
}

func TestSPAHandler_UpgradeWithOlderModTimeServesNewDocument(t *testing.T) {
	dir := t.TempDir()
	writeBuild := func(tag string, mtime time.Time) {
		for name, body := range map[string]string{
			"index.html":         "<!DOCTYPE html><html>index " + tag + "</html>",
			"auth-callback.html": "<!DOCTYPE html><html>callback " + tag + "</html>",
		} {
			p := filepath.Join(dir, name)
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
	}
	h := SPAHandler(os.DirFS(dir))

	previousBuild := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	writeBuild("A", previousBuild)
	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := first.Header().Get("Last-Modified"); got != "" {
		t.Fatalf("GET / on a real filesystem: Last-Modified = %q, want none", got)
	}

	writeBuild("B", previousBuild.Add(-24*time.Hour))
	for _, path := range []string{"/", "/index.html", "/auth-callback.html", "/deep/route"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("If-Modified-Since", previousBuild.Format(http.TimeFormat))
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("conditional GET %s after upgrade: got %d, want 200 (a 304 keeps the pre-upgrade document)", path, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, " B<") {
			t.Errorf("conditional GET %s after upgrade: body = %q, want the new build's document", path, body)
		}
	}
}

func TestSPAHandler_HashedAssetsOnDiskKeepLastModified(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "main-AbCd1234.js"), []byte("// js"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := SPAHandler(os.DirFS(dir))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/main-AbCd1234.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/main-AbCd1234.js: got %d, want 200", rec.Code)
	}
	if rec.Header().Get("Last-Modified") == "" {
		t.Error("GET /assets/main-AbCd1234.js: want Last-Modified from the file")
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("GET /assets/main-AbCd1234.js: Cache-Control = %q, want immutable", got)
	}
}

func TestSPAHandler_MissingBuildArtifact404s(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":             {Data: []byte("<!DOCTYPE html><html></html>")},
		"assets/main-NEW1234.js": {Data: []byte("// js")},
	}
	h := SPAHandler(fsys)

	for _, path := range []string{
		"/assets/main-OLD1234.js",
		"/assets/main-OLD1234.js.map",
		"/assets/chunk-OLD1234",
		"/vendor-OLD1234.js",
		"/style-OLD1234.css",
		"/font-OLD1234.woff2",
		"/favicon.ico",
		"/LOGO.SVG",
		"/ASSETS/main-OLD1234.js",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: got %d, want 404", path, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "<!DOCTYPE html>") {
			t.Errorf("GET %s: served index.html instead of 404ing; body = %q", path, body)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s: Cache-Control = %q, want %q so a rolling upgrade cannot retain it", path, got, "no-store")
		}
	}

	for _, ext := range buildArtifactExtensions {
		if !strings.HasPrefix(ext, ".") || ext != strings.ToLower(ext) {
			t.Errorf("buildArtifactExtensions has %q; entries must be lower case and start with a dot or they can never match", ext)
			continue
		}
		path := "/x-OLD1234" + ext
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: got %d, want 404", path, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/main-NEW1234.js", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /assets/main-NEW1234.js: got %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "// js" {
		t.Errorf("GET /assets/main-NEW1234.js: body = %q, want the asset", body)
	}
}

func TestSPAHandler_RoutesWithDotsStillServeIndex(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": {Data: []byte("<!DOCTYPE html><html></html>")},
	}
	h := SPAHandler(fsys)

	for _, path := range []string{
		"/agents/my.agent.name",
		"/chat/session-v1.2",
		"/agents/example.com",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200 (client-side route)", path, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "<!DOCTYPE html>") {
			t.Errorf("GET %s: want index.html, got %q", path, body)
		}
	}
}

func TestSPAHandler_APIRoutes404(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": {Data: []byte("<!DOCTYPE html>")},
	}
	h := SPAHandler(fsys)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/v1/health: got %d, want 404", rec.Code)
	}
}

func TestSPAHandler_ScriptsAreJavaScriptWhenTheOSMapsThemToPlainText(t *testing.T) {
	for _, ext := range []string{".js", ".mjs"} {
		original := mime.TypeByExtension(ext)
		if err := mime.AddExtensionType(ext, "text/plain"); err != nil {
			t.Fatalf("AddExtensionType(%s): %v", ext, err)
		}
		t.Cleanup(func() { _ = mime.AddExtensionType(ext, original) })
	}

	fsys := fstest.MapFS{
		"index.html":                         {Data: []byte("<!DOCTYPE html>")},
		"assets/pdf.worker.min-Dswkl-cV.mjs": {Data: []byte("// worker")},
		"assets/main-C36sWsWD.js":            {Data: []byte("// main")},
		"sw.js":                              {Data: []byte("// unhashed")},
		"module.mjs":                         {Data: []byte("// unhashed")},
	}
	h := SPAHandler(fsys)

	for _, p := range []string{"/assets/pdf.worker.min-Dswkl-cV.mjs", "/assets/main-C36sWsWD.js", "/sw.js", "/module.mjs"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != javaScriptContentType {
			t.Errorf("GET %s: Content-Type = %q, want %q", p, ct, javaScriptContentType)
		}
	}
}
