// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapispec

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestViewerPageRendersTheServedDescription(t *testing.T) {
	rec := httptest.NewRecorder()

	ViewerHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ViewerPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `spec-url="`+Path+`"`) {
		t.Errorf("the page does not load the description at %s", Path)
	}
	for _, asset := range ViewerAssets {
		if asset.Name == "solace-logo.png" {
			continue
		}
		if !strings.Contains(body, `"`+ViewerAssetPath(asset.Name)+`"`) {
			t.Errorf("the page does not load %s", ViewerAssetPath(asset.Name))
		}
	}
}

func TestViewerBundleIsTheVendoredRedocBundle(t *testing.T) {
	rec := httptest.NewRecorder()
	bundle := ViewerAssets[0]

	ViewerAssetHandler(bundle).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ViewerAssetPath(bundle.Name), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if !strings.Contains(rec.Body.String(), "spec-url") {
		t.Error("the served script is not the Redoc standalone bundle")
	}
}

func TestViewerServesEveryAssetItEmbeds(t *testing.T) {
	want := map[string]string{
		"redoc.standalone.js":   "text/javascript; charset=utf-8",
		"collapsible-groups.js": "text/javascript; charset=utf-8",
		"stylesheet.css":        "text/css; charset=utf-8",
		"solace-logo.png":       "image/png",
	}
	if len(ViewerAssets) != len(want) {
		t.Fatalf("ViewerAssets has %d entries, the test pins %d", len(ViewerAssets), len(want))
	}
	for _, asset := range ViewerAssets {
		rec := httptest.NewRecorder()

		ViewerAssetHandler(asset).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ViewerAssetPath(asset.Name), nil))

		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("%s: status %d, %d bytes", asset.Name, rec.Code, rec.Body.Len())
		}
		if got := rec.Header().Get("Content-Type"); got != want[asset.Name] {
			t.Errorf("%s: Content-Type = %q, want %q", asset.Name, got, want[asset.Name])
		}
		if got := rec.Header().Get("Content-Length"); got == "" {
			t.Errorf("%s: no Content-Length", asset.Name)
		}
	}
}

func TestViewerResponsesAreUncachedAndUnframeable(t *testing.T) {
	handlers := map[string]http.HandlerFunc{ViewerPath: ViewerHandler()}
	for _, asset := range ViewerAssets {
		handlers[ViewerAssetPath(asset.Name)] = ViewerAssetHandler(asset)
	}
	for path, handler := range handlers {
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", path, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
			t.Errorf("%s: Content-Security-Policy = %q", path, got)
		}
	}
}

func TestViewerPageSendsNoReferrer(t *testing.T) {
	rec := httptest.NewRecorder()

	ViewerHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ViewerPath, nil))

	if !strings.Contains(rec.Body.String(), `<meta name="referrer" content="no-referrer" />`) {
		t.Error("the page would send the deployment's origin to cdn.redoc.ly when Redoc fetches its logo")
	}
}

const redocBundleSHA256 = "dcaf76612bc4a3fbcc923a8966dee2f6146a5f32e5ce1b6f02dd60cbbf89500b"

func TestViewerBundleIsTheVendoredBytes(t *testing.T) {
	data, err := fs.ReadFile(viewer, viewerDir+"/redoc.standalone.js")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != redocBundleSHA256 {
		t.Fatalf("redoc.standalone.js sha256 = %s, want %s (see redoc/VERSION)", got, redocBundleSHA256)
	}
}

func TestViewerEmbedsNoLicenseOrVersionFiles(t *testing.T) {
	served := map[string]bool{viewerPage: true}
	for _, asset := range ViewerAssets {
		served[viewerDir+"/"+asset.Name] = true
	}
	entries, err := fs.ReadDir(viewer, viewerDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if name := viewerDir + "/" + entry.Name(); !served[name] {
			t.Errorf("%s would ship in every binary", name)
		}
	}
}
