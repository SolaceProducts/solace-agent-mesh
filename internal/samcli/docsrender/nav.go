// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"cmp"
	"encoding/json"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/frontmatter"
)

type NavItem struct {
	Label       string
	Route       string
	Description string
	Children    []*NavItem
	position    int
}

const posUnset = 1 << 30

type categoryMeta struct {
	Label    string `json:"label"`
	Position *int   `json:"position"`
	Link     struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"link"`
}

func (r *Renderer) BuildNav() ([]*NavItem, error) {
	return r.navForDir("")
}

func landingRoute(nav []*NavItem) string {
	if route, ok := firstNavRoute(nav); ok {
		return route
	}
	return urlPrefix
}

func firstNavRoute(nav []*NavItem) (string, bool) {
	for _, it := range nav {
		if it.Route != "" {
			return it.Route, true
		}
		if route, ok := firstNavRoute(it.Children); ok {
			return route, true
		}
	}
	return "", false
}

func (r *Renderer) navForDir(dir string) ([]*NavItem, error) {
	readDir := dir
	if readDir == "" {
		readDir = "."
	}
	entries, err := fs.ReadDir(r.fsys, readDir)
	if err != nil {
		return nil, err
	}

	var items []*NavItem
	for _, e := range entries {
		name := e.Name()
		full := path.Join(dir, name)
		switch {
		case e.IsDir():
			children, err := r.navForDir(full)
			if err != nil {
				return nil, err
			}
			cat := &NavItem{Label: defaultLabel(name), Children: children, position: posUnset}
			r.applyCategoryMeta(full, cat)
			if len(cat.Children) == 0 && cat.Route == "" {
				continue
			}
			items = append(items, cat)
		case strings.HasSuffix(name, ".md") && name != "index.md":
			label, pos, desc := r.pageMeta(full)
			items = append(items, &NavItem{
				Label:       label,
				Route:       routeForDoc(full),
				Description: desc,
				position:    pos,
			})
		}
	}

	slices.SortStableFunc(items, func(a, b *NavItem) int {
		if c := cmp.Compare(a.position, b.position); c != 0 {
			return c
		}
		return strings.Compare(a.Label, b.Label)
	})
	return items, nil
}

func (r *Renderer) applyCategoryMeta(dir string, cat *NavItem) {
	if data, err := fs.ReadFile(r.fsys, path.Join(dir, "_category_.json")); err == nil {
		var meta categoryMeta
		if json.Unmarshal(data, &meta) == nil {
			if meta.Label != "" {
				cat.Label = meta.Label
			}
			if meta.Position != nil {
				cat.position = *meta.Position
			}
			if meta.Link.Type == "doc" && meta.Link.ID != "" {
				if p := strings.TrimPrefix(meta.Link.ID, "documentation/") + ".md"; statFile(r.fsys, p) {
					cat.Route = routeForDoc(p)
				}
			}
		}
	}
	if idx := path.Join(dir, "index.md"); statFile(r.fsys, idx) {
		if cat.Route == "" {
			cat.Route = routeForDoc(idx)
		}
		_, idxPos, desc := r.pageMeta(idx)
		cat.Description = desc
		if cat.position == posUnset {
			cat.position = idxPos
		}
	}
}

func statFile(fsys fs.FS, p string) bool {
	info, err := fs.Stat(fsys, p)
	return err == nil && !info.IsDir()
}

func (r *Renderer) pageMeta(docPath string) (string, int, string) {
	label := defaultLabel(strings.TrimSuffix(path.Base(docPath), ".md"))
	pos := posUnset
	src, err := fs.ReadFile(r.fsys, docPath)
	if err != nil {
		return label, pos, ""
	}
	ctx := parser.NewContext()
	r.md.Parser().Parse(gmtext.NewReader(src), parser.WithContext(ctx))
	if fm := frontmatter.Get(ctx); fm != nil {
		var meta frontMatter
		if fm.Decode(&meta) == nil {
			if meta.Title != "" {
				label = meta.Title
			}
			if meta.SidebarPosition != nil {
				pos = *meta.SidebarPosition
			}
			return label, pos, meta.Description
		}
	}
	return label, pos, ""
}

func defaultLabel(name string) string {
	return strings.ReplaceAll(name, "-", " ")
}

func routeForDoc(docPath string) string {
	target := strings.TrimSuffix(docPath, ".md")
	target = strings.TrimSuffix(target, "/index")
	if target == "index" {
		target = ""
	}
	if target == "" {
		return urlPrefix
	}
	return urlPrefix + "/" + target
}
