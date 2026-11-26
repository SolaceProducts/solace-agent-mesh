// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"path"
	"strings"

	"github.com/yuin/goldmark/ast"
)

func dirOf(docPath string) string {
	d := path.Dir(docPath)
	if d == "." {
		return ""
	}
	return d
}

func rewriteLinks(doc ast.Node, baseDir string) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link, ok := n.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		if resolved, changed := ResolveLink(baseDir, string(link.Destination)); changed {
			link.Destination = []byte(resolved)
		}
		return ast.WalkContinue, nil
	})
}

func ResolveLink(baseDir, dest string) (string, bool) {
	if dest == "" {
		return dest, false
	}
	if strings.Contains(dest, "://") || strings.HasPrefix(dest, "#") ||
		strings.HasPrefix(dest, "mailto:") || strings.HasPrefix(dest, "/") {
		return dest, false
	}

	frag := ""
	if i := strings.IndexByte(dest, '#'); i >= 0 {
		frag = dest[i:]
		dest = dest[:i]
	}

	if !strings.HasSuffix(dest, ".md") {
		return dest + frag, false
	}

	target := path.Clean(path.Join(baseDir, dest))
	target = strings.TrimSuffix(target, ".md")
	target = strings.TrimSuffix(target, "/index")
	if target == "index" {
		target = ""
	}

	route := urlPrefix
	if target != "" {
		route += "/" + target
	}
	return route + frag, true
}
