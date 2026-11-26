// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	cacheControlHeader = "Cache-Control"

	uncachedCacheControl = "no-store"

	immutableCacheControl = "public, max-age=31536000, immutable"
)

var hashedAssetName = regexp.MustCompile(`^[^/]+-[A-Za-z0-9_-]{8}(?:\.[A-Za-z0-9]+)+$`)

func isImmutableAsset(urlPath string) bool {
	dir, name := path.Split(urlPath)
	return strings.EqualFold(dir, "/assets/") && hashedAssetName.MatchString(name)
}

var buildArtifactExtensions = []string{".js", ".mjs", ".css", ".map", ".ico", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".woff", ".woff2", ".ttf", ".wasm"}

func isBuildArtifact(urlPath string) bool {
	lowered := strings.ToLower(urlPath)
	if strings.HasPrefix(lowered, "/assets/") {
		return true
	}
	return slices.Contains(buildArtifactExtensions, path.Ext(lowered))
}

const javaScriptContentType = "text/javascript; charset=utf-8"

func setScriptContentType(w http.ResponseWriter, urlPath string) {
	switch strings.ToLower(path.Ext(urlPath)) {
	case ".js", ".mjs":
		w.Header().Set("Content-Type", javaScriptContentType)
	}
}

func isRegularFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && info.Mode().IsRegular()
}

func serveUncached(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()

	content, ok := f.(io.ReadSeeker)
	if !ok {
		data, readErr := io.ReadAll(f)
		if readErr != nil {
			http.Error(w, "failed to read "+name, http.StatusInternalServerError)
			return
		}
		content = bytes.NewReader(data)
	}
	w.Header().Set(cacheControlHeader, uncachedCacheControl)
	http.ServeContent(w, r, name, time.Time{}, content)
}

func SPAHandler(fsys fs.FS) http.Handler {
	assets := http.FileServer(http.FS(fsys))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		urlPath := r.URL.Path
		if urlPath == "/" {
			urlPath = "/index.html"
		}

		fsPath := strings.TrimPrefix(urlPath, "/")
		if isRegularFile(fsys, fsPath) {
			setScriptContentType(w, urlPath)
			if isImmutableAsset(urlPath) {
				w.Header().Set(cacheControlHeader, immutableCacheControl)
				assets.ServeHTTP(w, r)
				return
			}
			serveUncached(w, r, fsys, fsPath)
			return
		}

		if isBuildArtifact(urlPath) {
			w.Header().Set(cacheControlHeader, uncachedCacheControl)
			http.NotFound(w, r)
			return
		}

		serveUncached(w, r, fsys, "index.html")
	})
}
