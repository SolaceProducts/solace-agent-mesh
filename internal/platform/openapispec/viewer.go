// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapispec

import (
	"embed"
	"io"
	"net/http"
	"time"
)

const ViewerPath = "/redoc"

const (
	viewerDir  = "redoc"
	viewerPage = viewerDir + "/index.html"

	viewerCacheControl = "no-store"

	viewerContentSecurityPolicy = "frame-ancestors 'none'"
)

//go:embed redoc/index.html redoc/redoc.standalone.js redoc/collapsible-groups.js redoc/stylesheet.css redoc/solace-logo.png
var viewer embed.FS

type ViewerAsset struct {
	Name        string
	ContentType string
}

var ViewerAssets = []ViewerAsset{
	{"redoc.standalone.js", "text/javascript; charset=utf-8"},
	{"collapsible-groups.js", "text/javascript; charset=utf-8"},
	{"stylesheet.css", "text/css; charset=utf-8"},
	{"solace-logo.png", "image/png"},
}

func ViewerAssetPath(name string) string {
	return ViewerPath + "/" + name
}

func ViewerHandler() http.HandlerFunc {
	return serveFile(viewerPage, "text/html; charset=utf-8")
}

func ViewerAssetHandler(asset ViewerAsset) http.HandlerFunc {
	return serveFile(viewerDir+"/"+asset.Name, asset.ContentType)
}

func serveFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := viewer.Open(name)
		if err != nil {
			http.Error(w, "the API reference viewer is not embedded in this build", http.StatusInternalServerError)
			return
		}
		defer func() { _ = f.Close() }()
		content, ok := f.(io.ReadSeeker)
		if !ok {
			http.Error(w, "the API reference viewer is not embedded in this build", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", viewerCacheControl)
		w.Header().Set("Content-Security-Policy", viewerContentSecurityPolicy)
		http.ServeContent(w, r, name, time.Time{}, content)
	}
}
