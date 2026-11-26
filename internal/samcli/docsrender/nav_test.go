// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"testing"
	"testing/fstest"
)

func TestBuildNavSkipsAssetOnlyDirs(t *testing.T) {
	page := "---\ntitle: Try It\nsidebar_position: 1\n---\n# Try\n\nProse.\n"
	guideIndex := "---\ntitle: Guide\n---\n# Guide\n\nOverview.\n"
	r := New(fstest.MapFS{
		"intro.md":                     {Data: []byte(page)},
		"img/screenshot.png":           {Data: []byte("\x89PNG\r\n")},
		"getting-started/try.md":       {Data: []byte(page)},
		"getting-started/img/shot.png": {Data: []byte("\x89PNG\r\n")},
		"guide/index.md":               {Data: []byte(guideIndex)},
	})

	nav, err := r.BuildNav()
	if err != nil {
		t.Fatalf("BuildNav: %v", err)
	}

	labels := map[string]*NavItem{}
	for _, it := range nav {
		labels[it.Label] = it
		if it.Label == "img" {
			t.Errorf("top-level asset dir surfaced as nav category: %+v", it)
		}
	}

	if _, ok := labels["getting started"]; !ok {
		t.Fatalf("expected 'getting started' category; got %v", keys(labels))
	}
	for _, child := range labels["getting started"].Children {
		if child.Label == "img" {
			t.Errorf("nested asset dir surfaced as nav child under getting-started")
		}
	}

	if _, ok := labels["guide"]; !ok {
		t.Errorf("category with only an index.md should still appear; got %v", keys(labels))
	}
}

func TestOKFFrontmatterKeysKeepPageMeta(t *testing.T) {
	page := "---\npublished: true\ntitle: Create Your First Agent\ndescription: Build one.\n" +
		"sidebar_position: 150\ntype: tutorial\ntags: [product:agent-mesh, feature:ipc]\n---\n" +
		"# Create Your First Agent\n\nProse.\n"
	r := New(fstest.MapFS{"first.md": {Data: []byte(page)}})

	label, pos, desc := r.pageMeta("first.md")
	if label != "Create Your First Agent" || pos != 150 || desc != "Build one." {
		t.Fatalf("pageMeta = (%q, %d, %q), want title, 150 and description from frontmatter", label, pos, desc)
	}

	rendered, err := r.RenderFile("first.md")
	if err != nil {
		t.Fatalf("RenderFile: %v", err)
	}
	if rendered.Title != "Create Your First Agent" || rendered.Description != "Build one." {
		t.Fatalf("RenderFile title/description = %q/%q", rendered.Title, rendered.Description)
	}
}

func TestBuildNavCategoryLinkDoc(t *testing.T) {
	page := "---\ntitle: Connectors Overview\n---\n# Connectors\n\nProse.\n"
	child := "---\ntitle: A Child\n---\n# Child\n\nProse.\n"
	r := New(fstest.MapFS{
		"connectors/_category_.json": {Data: []byte(
			`{"label":"Connectors","link":{"type":"doc","id":"documentation/connectors/connectors"}}`)},
		"connectors/connectors.md": {Data: []byte(page)},
		"connectors/child.md":      {Data: []byte(child)},
		"gateways/_category_.json": {Data: []byte(
			`{"label":"Gateways","link":{"type":"doc","id":"documentation/gateways/missing"}}`)},
		"gateways/child.md": {Data: []byte(child)},
		"custom/_category_.json": {Data: []byte(
			`{"label":"Custom","link":{"type":"generated-index"}}`)},
		"custom/child.md": {Data: []byte(child)},
	})

	nav, err := r.BuildNav()
	if err != nil {
		t.Fatalf("BuildNav: %v", err)
	}
	byLabel := map[string]*NavItem{}
	for _, it := range nav {
		byLabel[it.Label] = it
	}

	if c := byLabel["Connectors"]; c == nil {
		t.Fatalf("Connectors category missing; got %v", keys(byLabel))
	} else if want := routeForDoc("connectors/connectors.md"); c.Route != want {
		t.Errorf("Connectors Route = %q, want %q (link:doc should be clickable)", c.Route, want)
	}
	if g := byLabel["Gateways"]; g == nil {
		t.Fatalf("Gateways category missing; got %v", keys(byLabel))
	} else if g.Route != "" {
		t.Errorf("Gateways Route = %q, want empty (dangling link target)", g.Route)
	}
	if cu := byLabel["Custom"]; cu == nil {
		t.Fatalf("Custom category missing; got %v", keys(byLabel))
	} else if cu.Route != "" {
		t.Errorf("Custom Route = %q, want empty (generated-index is not a doc link)", cu.Route)
	}
}

func TestBuildNavCategoryLinkDocPrecedence(t *testing.T) {
	page := "---\ntitle: P\n---\n# P\n\nProse.\n"
	r := New(fstest.MapFS{
		"both/_category_.json": {Data: []byte(
			`{"label":"Both","link":{"type":"doc","id":"documentation/both/page"}}`)},
		"both/page.md":  {Data: []byte(page)},
		"both/index.md": {Data: []byte(page)},
		"empty/_category_.json": {Data: []byte(
			`{"label":"Empty","link":{"type":"doc","id":""}}`)},
		"empty/index.md": {Data: []byte(page)},
		"noprefix/_category_.json": {Data: []byte(
			`{"label":"NoPrefix","link":{"type":"doc","id":"nowhere/page"}}`)},
		"noprefix/child.md": {Data: []byte(page)},
	})

	nav, err := r.BuildNav()
	if err != nil {
		t.Fatalf("BuildNav: %v", err)
	}
	byLabel := map[string]*NavItem{}
	for _, it := range nav {
		byLabel[it.Label] = it
	}

	if b := byLabel["Both"]; b == nil {
		t.Fatalf("Both category missing; got %v", keys(byLabel))
	} else if want := routeForDoc("both/page.md"); b.Route != want {
		t.Errorf("Both Route = %q, want %q (explicit link:doc must win over index.md)", b.Route, want)
	}
	if e := byLabel["Empty"]; e == nil {
		t.Fatalf("Empty category missing; got %v", keys(byLabel))
	} else if want := routeForDoc("empty/index.md"); e.Route != want {
		t.Errorf("Empty Route = %q, want %q (empty id ignored, index.md drives route)", e.Route, want)
	}
	if n := byLabel["NoPrefix"]; n == nil {
		t.Fatalf("NoPrefix category missing; got %v", keys(byLabel))
	} else if n.Route != "" {
		t.Errorf("NoPrefix Route = %q, want empty (unresolvable id falls back to non-clickable)", n.Route)
	}
}

func TestNewServerRejectsNilFilesystem(t *testing.T) {
	s, err := NewServer(nil)
	if err == nil {
		t.Fatal("NewServer(nil) returned no error; a nil filesystem must be rejected rather than reaching fs.ReadDir and panicking")
	}
	if s != nil {
		t.Errorf("NewServer(nil) returned server %+v, want nil", s)
	}
}

func keys(m map[string]*NavItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
