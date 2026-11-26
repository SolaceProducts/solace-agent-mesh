// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package publisheddocs

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SolaceProducts/solace-agent-mesh/internal/aiassist"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/docsrender"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

const docsRoute = "/docs/documentation"

func page(frontmatter, title string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("---\n" + frontmatter + "title: " + title + "\n---\n\n# " + title + "\n\n" + title + " body.\n")}
}

func docsTree() fstest.MapFS {
	return fstest.MapFS{
		"intro.md":               page("published: true\nsidebar_position: 1\n", "Intro Page"),
		"draft.md":               page("published: false\n", "Draft Page"),
		"nokey.md":               page("", "No Key Page"),
		"guide/_category_.json":  {Data: []byte(`{"label": "Guide"}`)},
		"guide/index.md":         page("published: true\n", "Guide Home"),
		"guide/deploy.md":        page("published: true\n", "Deploy Page"),
		"secret/_category_.json": {Data: []byte(`{"label": "Secret Section"}`)},
		"secret/index.md":        page("published: false\n", "Secret Home"),
		"secret/deep.md":         page("published: true\n", "Deep Secret Page"),
		"secret/img/shot.png":    {Data: []byte("png")},
		"ops/ops.md":             page("", "Ops Home"),
		"ops/run.md":             page("published: true\n", "Run Book Page"),
		"mixed/mixed.md":         page("published: true\n", "Mixed Home"),
		"mixed/index.md":         page("published: false\n", "Mixed Index"),
		"mixed/child.md":         page("published: true\n", "Mixed Child"),
	}
}

var publishedFiles = []string{
	"guide/_category_.json",
	"guide/deploy.md",
	"guide/index.md",
	"intro.md",
	"mixed/child.md",
	"mixed/mixed.md",
}

var withheldPaths = []string{
	"draft.md",
	"nokey.md",
	"secret",
	"secret/_category_.json",
	"secret/index.md",
	"secret/deep.md",
	"secret/img",
	"secret/img/shot.png",
	"ops",
	"ops/ops.md",
	"ops/run.md",
	"mixed/index.md",
}

var withheldTitles = []string{
	"Draft Page",
	"No Key Page",
	"Secret Section",
	"Secret Home",
	"Deep Secret Page",
	"Ops Home",
	"Run Book Page",
	"Mixed Index",
}

func walkFiles(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var files []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	slices.Sort(files)
	return files
}

func TestPublishedViewListsOnlyPublishedFiles(t *testing.T) {
	got := walkFiles(t, FS(docsTree()))
	if !slices.Equal(got, publishedFiles) {
		t.Fatalf("published view lists %v, want only %v", got, publishedFiles)
	}
}

func TestPublishedViewWithholdsUnpublishedPagesAndHiddenSections(t *testing.T) {
	view := FS(docsTree())
	for _, p := range withheldPaths {
		if _, err := fs.Stat(view, p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) err = %v, want fs.ErrNotExist", p, err)
		}
		if _, err := view.Open(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Open(%q) err = %v, want fs.ErrNotExist", p, err)
		}
	}
	for _, dir := range []string{"secret", "ops"} {
		if _, err := fs.ReadDir(view, dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadDir(%q) err = %v, want fs.ErrNotExist", dir, err)
		}
	}
}

func TestPublishedViewIsAConsistentFS(t *testing.T) {
	if err := fstest.TestFS(FS(docsTree()), publishedFiles...); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedViewRequiresPublishedTrue(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		visible bool
	}{
		{"lowercase true", "---\npublished: true\n---\n# P\n", true},
		{"capitalized true", "---\npublished: True\n---\n# P\n", true},
		{"uppercase true", "---\npublished: TRUE\n---\n# P\n", true},
		{"double-quoted true", "---\npublished: \"true\"\n---\n# P\n", true},
		{"single-quoted true", "---\npublished: 'true'\n---\n# P\n", true},
		{"CRLF line endings", "---\r\npublished: true\r\n---\r\n# P\r\n", true},
		{"false", "---\npublished: false\n---\n# P\n", false},
		{"yes is not true", "---\npublished: yes\n---\n# P\n", false},
		{"empty value", "---\npublished:\n---\n# P\n", false},
		{"key missing", "---\ntitle: P\n---\n# P\n", false},
		{"no frontmatter", "# P\n\npublished: true\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := FS(fstest.MapFS{"p.md": {Data: []byte(tc.source)}})
			_, err := fs.Stat(view, "p.md")
			if visible := err == nil; visible != tc.visible {
				t.Fatalf("page visible = %v (err %v), want %v", visible, err, tc.visible)
			}
		})
	}
}

func preflightFixture(t *testing.T, name string) fs.FS {
	t.Helper()
	dir := filepath.Join("..", "..", "cmd", "docs-preflight", "testdata", name)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("docs-preflight fixture %s: %v", name, err)
	}
	return os.DirFS(dir)
}

func TestPublishedViewMatchesDocsPreflightFixtures(t *testing.T) {
	sourcetree.RequireInternal(t)
	cases := []struct {
		fixture string
		visible []string
	}{
		{"valid", []string{"clean.md"}},
		{"missing-published", nil},
		{"link-to-hidden", []string{"visible.md"}},
		{"link-to-missing-published", []string{"visible.md"}},
		{"link-to-hidden-section", []string{"visible.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			got := walkFiles(t, FS(preflightFixture(t, tc.fixture)))
			if !slices.Equal(got, tc.visible) {
				t.Fatalf("published view of docs-preflight fixture %s lists %v, want %v", tc.fixture, got, tc.visible)
			}
		})
	}
}

func TestDocsServerLinkFromPublishedToUnpublishedPageIsNotFound(t *testing.T) {
	sourcetree.RequireInternal(t)
	cases := []struct {
		fixture string
		target  string
	}{
		{"link-to-hidden", "/hidden"},
		{"link-to-missing-published", "/orphan"},
		{"link-to-hidden-section", "/sec/page"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			srv, err := docsrender.NewServer(FS(preflightFixture(t, tc.fixture)))
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			h := srv.Handler()
			code, body := get(t, h, docsRoute+"/visible")
			if code != http.StatusOK {
				t.Fatalf("published page: status %d, want 200", code)
			}
			if !strings.Contains(body, `href="`+docsRoute+tc.target+`"`) {
				t.Fatalf("published page does not link to %s, so the scenario is not exercised", tc.target)
			}
			if code, _ := get(t, h, docsRoute+tc.target); code != http.StatusNotFound {
				t.Errorf("link target %s: status %d, want 404 because docs-preflight withholds it from the build", tc.target, code)
			}
		})
	}
}

func get(t *testing.T, h http.Handler, url string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return rec.Code, string(body)
}

func TestDocsServerOmitsUnpublishedPages(t *testing.T) {
	srv, err := docsrender.NewServer(FS(docsTree()))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	for _, route := range []string{"/intro", "/intro.md", "/guide/deploy", "/mixed/child"} {
		if code, _ := get(t, h, docsRoute+route); code != http.StatusOK {
			t.Errorf("published route %s: status %d, want 200", route, code)
		}
	}
	for _, route := range []string{
		"/draft", "/draft.md", "/nokey", "/secret", "/secret.md", "/secret/deep", "/secret/deep.md",
		"/secret/img/shot.png", "/ops", "/ops/ops", "/ops/run", "/ops/run.md",
	} {
		if code, _ := get(t, h, docsRoute+route); code != http.StatusNotFound {
			t.Errorf("unpublished route %s: status %d, want 404", route, code)
		}
	}

	listings := map[string]string{
		"sidebar of a published page": docsRoute + "/intro",
		"search index":                docsRoute + "/search-index.json",
		"llms.txt":                    "/llms.txt",
		"llms-full.txt":               "/llms-full.txt",
	}
	for surface, url := range listings {
		code, body := get(t, h, url)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", surface, code)
		}
		if !strings.Contains(body, "Deploy Page") {
			t.Errorf("%s does not list the published page %q", surface, "Deploy Page")
		}
		for _, title := range withheldTitles {
			if strings.Contains(body, title) {
				t.Errorf("%s exposes unpublished content %q", surface, title)
			}
		}
	}
}

func TestSkillInstallOmitsUnpublishedPages(t *testing.T) {
	dir := t.TempDir()
	if err := aiassist.InstallSuite(dir, "v1", false, FS(docsTree())); err != nil {
		t.Fatalf("InstallSuite: %v", err)
	}
	corpus := filepath.Join(dir, "sam-docs", "references", "documentation")
	got := walkFiles(t, os.DirFS(corpus))
	if !slices.Equal(got, publishedFiles) {
		t.Fatalf("sam-docs corpus holds %v, want only the published files %v", got, publishedFiles)
	}
}
