// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

const llmsIntro = "# Solace Agent Mesh Documentation\n\n" +
	"> Offline documentation for Solace Agent Mesh, served locally by `sam docs`. " +
	"Each link returns the raw markdown source of a page; fetch `/llms-full.txt` for the whole corpus in one request.\n"

func (s *Server) serveLLMS(w http.ResponseWriter, r *http.Request) {
	origin := "http://" + r.Host
	var b strings.Builder
	b.WriteString(llmsIntro)

	var cats []*NavItem
	var lead []*NavItem
	for _, it := range s.nav {
		if len(it.Children) == 0 {
			lead = append(lead, it)
		} else {
			cats = append(cats, it)
		}
	}
	if len(lead) > 0 {
		b.WriteByte('\n')
		for _, it := range lead {
			writeLLMSLink(&b, origin, it)
		}
	}
	for _, cat := range cats {
		fmt.Fprintf(&b, "\n## %s\n", cat.Label)
		writeLLMSItems(&b, origin, cat)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func writeLLMSItems(b *strings.Builder, origin string, cat *NavItem) {
	if cat.Route != "" {
		writeLLMSLink(b, origin, cat)
	}
	for _, child := range cat.Children {
		if len(child.Children) == 0 {
			writeLLMSLink(b, origin, child)
			continue
		}
		writeLLMSItems(b, origin, child)
	}
}

func writeLLMSLink(b *strings.Builder, origin string, it *NavItem) {
	if it.Route == "" {
		return
	}
	url := origin + it.Route + ".md"
	if it.Description != "" {
		fmt.Fprintf(b, "- [%s](%s) - %s\n", it.Label, url, it.Description)
		return
	}
	fmt.Fprintf(b, "- [%s](%s)\n", it.Label, url)
}

func (s *Server) serveLLMSFull(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(s.llmsFull)
}

func (s *Server) buildLLMSFull() []byte {
	var b strings.Builder
	for _, docPath := range s.navDocPaths() {
		data, err := fs.ReadFile(s.fsys, docPath)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "<!-- ===== %s ===== -->\n\n", docPath)
		b.Write(data)
		if !strings.HasSuffix(string(data), "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func (s *Server) navDocPaths() []string {
	var paths []string
	seen := make(map[string]bool)
	var walk func(items []*NavItem)
	walk = func(items []*NavItem) {
		for _, it := range items {
			if it.Route != "" {
				tail := strings.Trim(strings.TrimPrefix(it.Route, urlPrefix), "/")
				if dp, ok := s.resolveDoc(tail); ok && !seen[dp] {
					seen[dp] = true
					paths = append(paths, dp)
				}
			}
			walk(it.Children)
		}
	}
	walk(s.nav)
	return paths
}
