// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func synthDocs() fstest.MapFS {
	intro := "---\ntitle: Getting Started\nsidebar_position: 1\n---\n" +
		"# Welcome\n\nRun the binary with app_exec configured.\n\n" +
		"```bash\nsam run --sam-home /tmp\n```\n"
	guideIndex := "---\ntitle: Guide Home\n---\n# Guide\n\nOverview prose.\n"
	deploy := "---\ntitle: Deploying\n---\n# Deploy\n\nSet SAM_FEATURE_X to toggle.\n"
	return fstest.MapFS{
		"intro.md":        {Data: []byte(intro)},
		"guide/index.md":  {Data: []byte(guideIndex)},
		"guide/deploy.md": {Data: []byte(deploy)},
	}
}

func indexByRoute(docs []searchDoc) map[string]searchDoc {
	m := make(map[string]searchDoc, len(docs))
	for _, d := range docs {
		m[d.Route] = d
	}
	return m
}

func TestBuildSearchIndex(t *testing.T) {
	r := New(synthDocs())
	docs, err := r.BuildSearchIndex()
	if err != nil {
		t.Fatalf("BuildSearchIndex: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("indexed %d pages, want 3", len(docs))
	}
	byRoute := indexByRoute(docs)

	intro, ok := byRoute["/docs/documentation/intro"]
	if !ok {
		t.Fatal("intro page not indexed")
	}
	if intro.Title != "Getting Started" {
		t.Errorf("intro title = %q, want %q", intro.Title, "Getting Started")
	}
	for _, want := range []string{"welcome", "app_exec", "sam run", "--sam-home"} {
		if !strings.Contains(intro.Text, want) {
			t.Errorf("intro text missing %q; got: %q", want, intro.Text)
		}
	}

	if _, present := byRoute["/docs/documentation/guide"]; !present {
		t.Errorf("guide/index.md not indexed at directory route; routes: %v", byRoute)
	}

	deploy, ok := byRoute["/docs/documentation/guide/deploy"]
	if !ok {
		t.Fatal("guide/deploy.md not indexed")
	}
	if !strings.Contains(deploy.Text, "sam_feature_x") {
		t.Errorf("deploy text missing lowercased code token; got: %q", deploy.Text)
	}
}

func TestSearchIndexCoversEveryPage(t *testing.T) {
	fsys := docsSourceFS(t)
	r := New(fsys)
	docs, err := r.BuildSearchIndex()
	if err != nil {
		t.Fatalf("BuildSearchIndex: %v", err)
	}

	var pages int
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			pages++
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if pages == 0 {
		t.Fatal("no markdown pages found")
	}
	if len(docs) != pages {
		t.Errorf("indexed %d records, walk found %d .md files", len(docs), pages)
	}
	for _, d := range docs {
		if d.Title == "" || d.Route == "" {
			t.Errorf("record with empty title/route: %+v", d)
		}
	}
}

func TestSearchRoutesServed(t *testing.T) {
	srv, err := NewServer(synthDocs())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	t.Run("index json", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, searchIndexRoute, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		var docs []searchDoc
		if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
			t.Fatalf("unmarshal index: %v", err)
		}
		if len(docs) != 3 {
			t.Errorf("served %d records, want 3", len(docs))
		}
	})

	t.Run("lunr asset", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, lunrRoute, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Errorf("content-type = %q, want javascript", ct)
		}
		if !strings.Contains(rec.Body.String(), "lunr") {
			t.Error("lunr asset body does not mention lunr")
		}
	})
}

func TestLunrAssetIntegrity(t *testing.T) {
	sum := sha256.Sum256(lunrJS)
	if got := hex.EncodeToString(sum[:]); got != lunrJSSHA256 {
		t.Fatalf("lunr.min.js sha256 = %s, want %s (blob changed — see server.go provenance comment)", got, lunrJSSHA256)
	}
}
