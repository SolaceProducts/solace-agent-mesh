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

func shellDocs() fstest.MapFS {
	intro := "---\ntitle: Getting Started\nsidebar_position: 1\ndescription: Start here.\n---\n" +
		"# Welcome\n\nIntro prose.\n\n" +
		":::note Heads up\nA noted thing.\n:::\n\n" +
		":::tip\nA tip with the default title.\n:::\n\n" +
		"## First Section\n\nProse one.\n\n" +
		"### A Subsection\n\nProse two.\n\n" +
		"## Second Section\n\nProse three.\n\n" +
		"```mermaid\ngraph TD; A-->B;\n```\n"
	guideIndex := "---\ntitle: Guide Home\n---\n# Guide\n\nGuide overview.\n"
	deploy := "---\ntitle: Deploying\n---\n# Deploy\n\nDeploy prose.\n"
	return fstest.MapFS{
		"intro.md":              {Data: []byte(intro)},
		"guide/_category_.json": {Data: []byte(`{"label":"User Guide","position":2}`)},
		"guide/index.md":        {Data: []byte(guideIndex)},
		"guide/deploy.md":       {Data: []byte(deploy)},
		"img/logo.svg":          {Data: []byte("<svg></svg>")},
	}
}

func getBody(t *testing.T, h http.Handler, url string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec.Code, rec.Body.String()
}

func TestServePageRendersShell(t *testing.T) {
	srv, err := NewServer(shellDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	code, body := getBody(t, h, urlPrefix+"/intro")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	for _, want := range []string{
		"<!DOCTYPE html>",
		"<title>Getting Started",
		`<meta name="description" content="Start here.">`,
		"Intro prose.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}

	if !strings.Contains(body, `class="nav-link active"`) {
		t.Error("active nav link not marked active")
	}
	if !strings.Contains(body, "User Guide") {
		t.Error("category label from _category_.json missing from nav")
	}
	if !strings.Contains(body, `<li class="nav-cat collapsed">`) {
		t.Error("inactive category should render collapsed")
	}

	for _, want := range []string{"On this page", "First Section", "A Subsection", "Second Section"} {
		if !strings.Contains(body, want) {
			t.Errorf("TOC missing %q", want)
		}
	}

	if !strings.Contains(body, `<div class="admonition admonition-note">`) ||
		!strings.Contains(body, `<div class="admonition-title">Heads up</div>`) {
		t.Error("note admonition with explicit title not rendered")
	}
	if !strings.Contains(body, `<div class="admonition admonition-tip">`) ||
		!strings.Contains(body, `<div class="admonition-title">Tip</div>`) {
		t.Error("tip admonition with default title not rendered")
	}

	if !strings.Contains(body, `<div class="mermaid">`) {
		t.Error("mermaid block not rendered")
	}

	if !strings.Contains(body, `data-url="`+urlPrefix+`/intro.md"`) {
		t.Error("copy-page button missing markdown route")
	}
}

func TestServeNavExpandsActiveCategory(t *testing.T) {
	srv, err := NewServer(shellDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	code, body := getBody(t, h, urlPrefix+"/guide/deploy")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if strings.Contains(body, `<li class="nav-cat collapsed">`) {
		t.Error("category containing the active page should be expanded")
	}
	if !strings.Contains(body, `<li class="nav-cat">`) {
		t.Error("expected an expanded category list item")
	}
}

func TestServeTOCOmittedForShortPage(t *testing.T) {
	srv, err := NewServer(shellDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	code, body := getBody(t, srv.Handler(), urlPrefix+"/guide")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if strings.Contains(body, "On this page") {
		t.Error("TOC panel should be omitted for a single-section page")
	}
}

func TestServeAsset(t *testing.T) {
	srv, err := NewServer(shellDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	code, body := getBody(t, srv.Handler(), urlPrefix+"/img/logo.svg")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(body, "<svg>") {
		t.Errorf("asset body = %q, want the svg bytes", body)
	}
}

func TestServeNotFound(t *testing.T) {
	srv, err := NewServer(shellDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	code, _ := getBody(t, srv.Handler(), urlPrefix+"/does-not-exist")
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestServeRedirects(t *testing.T) {
	srv, err := NewServer(shellDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	for _, url := range []string{"/", urlPrefix, urlPrefix + "/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusFound {
			t.Errorf("GET %q: status = %d, want 302", url, rec.Code)
			continue
		}
		if loc := rec.Header().Get("Location"); loc != srv.IntroRoute() {
			t.Errorf("GET %q: Location = %q, want %q", url, loc, srv.IntroRoute())
		}
	}
}

func TestServeRootNonSlashNotFound(t *testing.T) {
	srv, err := NewServer(shellDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	code, _ := getBody(t, srv.Handler(), "/favicon.ico")
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}
