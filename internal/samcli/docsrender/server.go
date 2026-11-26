// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed static/lunr.min.js
var lunrJS []byte

const lunrJSSHA256 = "0c50d9002b85780a842afffb567bb54ede402dae7c6dc5997a018614d8044fc8"

var (
	searchIndexRoute = urlPrefix + "/search-index.json"
	lunrRoute        = urlPrefix + "/lunr.min.js"
)

const (
	llmsRoute     = "/llms.txt"
	llmsFullRoute = "/llms-full.txt"
)

const contentTypeHeader = "Content-Type"

type Server struct {
	renderer    *Renderer
	fsys        fs.FS
	nav         []*NavItem
	introRoute  string
	searchIndex []byte
	llmsFull    []byte
}

func NewServer(fsys fs.FS) (*Server, error) {
	if fsys == nil {
		return nil, fmt.Errorf("no documentation filesystem")
	}
	r := New(fsys)
	nav, err := r.BuildNav()
	if err != nil {
		return nil, err
	}
	docs, err := r.BuildSearchIndex()
	if err != nil {
		return nil, err
	}
	index, err := json.Marshal(docs)
	if err != nil {
		return nil, fmt.Errorf("marshal search index: %w", err)
	}
	s := &Server{renderer: r, fsys: fsys, nav: nav, introRoute: landingRoute(nav), searchIndex: index}
	s.llmsFull = s.buildLLMSFull()
	return s, nil
}

func (s *Server) IntroRoute() string { return s.introRoute }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(searchIndexRoute, s.serveSearchIndex)
	mux.HandleFunc(lunrRoute, s.serveLunr)
	mux.HandleFunc(llmsRoute, s.serveLLMS)
	mux.HandleFunc(llmsFullRoute, s.serveLLMSFull)
	mux.HandleFunc(urlPrefix+"/", s.serve)
	mux.HandleFunc(urlPrefix, s.serve)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, s.IntroRoute(), http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	return mux
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, urlPrefix)
	rel = strings.Trim(rel, "/")
	if rel == "" {
		if strings.Trim(strings.TrimPrefix(s.IntroRoute(), urlPrefix), "/") == "" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, s.IntroRoute(), http.StatusFound)
		return
	}

	if strings.HasSuffix(rel, ".md") {
		docPath, ok := s.resolveDoc(strings.TrimSuffix(rel, ".md"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.serveMarkdownSource(w, r, docPath)
		return
	}

	if ext := path.Ext(rel); ext != "" {
		s.serveAsset(w, r, rel)
		return
	}

	docPath, ok := s.resolveDoc(rel)
	if !ok {
		http.NotFound(w, r)
		return
	}

	page, err := s.renderer.RenderFile(docPath)
	if err != nil {
		http.Error(w, "failed to render documentation page", http.StatusInternalServerError)
		return
	}
	w.Header().Set(contentTypeHeader, "text/html; charset=utf-8")
	_ = s.renderer.WritePage(w, page, s.nav, urlPrefix+"/"+rel)
}

func (s *Server) resolveDoc(rel string) (string, bool) {
	rel = path.Clean(rel)
	for _, cand := range []string{rel + ".md", path.Join(rel, "index.md")} {
		if info, err := fs.Stat(s.fsys, cand); err == nil && !info.IsDir() {
			return cand, true
		}
	}
	return "", false
}

func (s *Server) serveMarkdownSource(w http.ResponseWriter, r *http.Request, docPath string) {
	data, err := fs.ReadFile(s.fsys, docPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set(contentTypeHeader, "text/markdown; charset=utf-8")
	http.ServeContent(w, r, path.Base(docPath), time.Time{}, bytes.NewReader(data))
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, rel string) {
	rel = path.Clean(rel)
	data, err := fs.ReadFile(s.fsys, rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path.Base(rel), time.Time{}, bytes.NewReader(data))
}

func (s *Server) serveSearchIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(contentTypeHeader, "application/json; charset=utf-8")
	_, _ = w.Write(s.searchIndex)
}

func (s *Server) serveLunr(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(contentTypeHeader, "application/javascript; charset=utf-8")
	http.ServeContent(w, r, "lunr.min.js", time.Time{}, bytes.NewReader(lunrJS))
}
