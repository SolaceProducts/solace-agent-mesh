// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package publisheddocs

import (
	"io"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

var frontmatterRE = regexp.MustCompile(`(?s)\A---\s*\n(.*?)\n---\s*\n`)

type publishedFS struct {
	fsys fs.FS
}

func FS(fsys fs.FS) fs.FS {
	return publishedFS{fsys: fsys}
}

func (p publishedFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if p.withheld(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	f, err := p.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	dir, listable := f.(fs.ReadDirFile)
	if !listable {
		return f, nil
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.IsDir() {
		return f, nil
	}
	return &publishedDir{ReadDirFile: dir, view: p, name: name}, nil
}

func (p publishedFS) visibleEntries(dir string, entries []fs.DirEntry) []fs.DirEntry {
	visible := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		if !p.withheld(path.Join(dir, e.Name())) {
			visible = append(visible, e)
		}
	}
	return visible
}

func (p publishedFS) withheld(name string) bool {
	if path.Ext(name) == ".md" && !p.pagePublished(name) {
		return true
	}
	for section := name; section != "."; section = path.Dir(section) {
		if index := p.sectionIndex(section); index != "" && !p.pagePublished(index) {
			return true
		}
	}
	return false
}

func (p publishedFS) sectionIndex(dir string) string {
	for _, candidate := range []string{path.Join(dir, path.Base(dir)+".md"), path.Join(dir, "index.md")} {
		if info, err := fs.Stat(p.fsys, candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func (p publishedFS) pagePublished(name string) bool {
	src, err := fs.ReadFile(p.fsys, name)
	if err != nil {
		return false
	}
	return frontmatterPublished(src)
}

func frontmatterPublished(src []byte) bool {
	m := frontmatterRE.FindSubmatch(src)
	if m == nil {
		return false
	}
	for line := range strings.SplitSeq(string(m[1]), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "published" {
			continue
		}
		value = strings.Trim(strings.Trim(strings.TrimSpace(value), `"`), `'`)
		return strings.EqualFold(value, "true")
	}
	return false
}

type publishedDir struct {
	fs.ReadDirFile
	view    publishedFS
	name    string
	pending []fs.DirEntry
	listed  bool
}

func (d *publishedDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if !d.listed {
		all, err := d.ReadDirFile.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		d.pending = d.view.visibleEntries(d.name, all)
		d.listed = true
	}
	if n <= 0 {
		rest := d.pending
		d.pending = nil
		return rest, nil
	}
	if len(d.pending) == 0 {
		return nil, io.EOF
	}
	batch := d.pending[:min(n, len(d.pending))]
	d.pending = d.pending[len(batch):]
	return batch, nil
}
