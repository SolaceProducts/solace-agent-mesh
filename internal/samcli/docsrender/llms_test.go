// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func llmsDocs() fstest.MapFS {
	intro := "---\ntitle: Getting Started\nsidebar_position: 1\ndescription: Start here.\n---\n# Welcome\n\nIntro prose.\n"
	guideIndex := "---\ntitle: Guide Home\ndescription: The guide.\n---\n# Guide\n\nGuide overview.\n"
	deploy := "---\ntitle: Deploying\n---\n# Deploy\n\nDeploy prose.\n"
	return fstest.MapFS{
		"intro.md":        {Data: []byte(intro)},
		"guide/index.md":  {Data: []byte(guideIndex)},
		"guide/deploy.md": {Data: []byte(deploy)},
	}
}

func TestMarkdownPassthrough(t *testing.T) {
	srv, err := NewServer(llmsDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	cases := []struct {
		name string
		url  string
		want string
	}{
		{"leaf page", urlPrefix + "/intro.md", "# Welcome"},
		{"category index page", urlPrefix + "/guide.md", "# Guide"},
		{"nested leaf", urlPrefix + "/guide/deploy.md", "# Deploy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
				t.Errorf("content-type = %q, want text/markdown", ct)
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("body missing %q; got: %q", tc.want, body)
			}
			if strings.Contains(body, "<h1") {
				t.Errorf("served rendered HTML, want raw markdown; got: %q", body)
			}
		})
	}

	t.Run("missing page 404s", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, urlPrefix+"/nope.md", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}

func TestLLMSIndex(t *testing.T) {
	srv, err := NewServer(llmsDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, llmsRoute, nil)
	req.Host = "localhost:9123"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	if !strings.Contains(body, "(http://localhost:9123"+urlPrefix+"/intro.md)") {
		t.Errorf("intro link not absolute from Host; got:\n%s", body)
	}
	if !strings.Contains(body, "[Getting Started](http://localhost:9123"+urlPrefix+"/intro.md) - Start here.") {
		t.Errorf("intro line missing description suffix; got:\n%s", body)
	}
	if !strings.Contains(body, urlPrefix+"/guide/deploy.md)\n") || strings.Contains(body, "deploy.md) - ") {
		t.Errorf("deploy line should have no description suffix; got:\n%s", body)
	}
	if !strings.Contains(body, "## Guide Home") && !strings.Contains(body, "## guide") {
		t.Errorf("category section heading missing; got:\n%s", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q, want text/plain", ct)
	}
}

func TestLLMSFull(t *testing.T) {
	srv, err := NewServer(llmsDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, llmsFullRoute, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		"<!-- ===== intro.md ===== -->", "Intro prose.",
		"<!-- ===== guide/index.md ===== -->", "Guide overview.",
		"<!-- ===== guide/deploy.md ===== -->", "Deploy prose.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("corpus missing %q", want)
		}
	}
	introAt := strings.Index(body, "intro.md")
	guideAt := strings.Index(body, "guide/index.md")
	deployAt := strings.Index(body, "guide/deploy.md")
	if introAt >= guideAt || guideAt >= deployAt {
		t.Errorf("corpus not in nav order: intro=%d guide=%d deploy=%d", introAt, guideAt, deployAt)
	}
}
