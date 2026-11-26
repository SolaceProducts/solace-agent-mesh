// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapispec

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestServeReturnsTheEmbeddedDescription(t *testing.T) {
	files := fstest.MapFS{"spec/sam-openapi.yaml": {Data: []byte("openapi: 3.0.3\n")}}
	rec := httptest.NewRecorder()

	serve(files, slog.New(slog.DiscardHandler)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/yaml" {
		t.Errorf("Content-Type = %q, want application/yaml", got)
	}
	if rec.Body.String() != "openapi: 3.0.3\n" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestServeFailsLoudlyWhenNotGenerated(t *testing.T) {
	rec := httptest.NewRecorder()

	serve(fstest.MapFS{"spec/.gitkeep": {}}, slog.New(slog.DiscardHandler)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "make generate") {
		t.Errorf("body %q must tell the operator how to fix it", rec.Body.String())
	}
}

func TestAvailableIsTrueOnlyWithTheDescriptionEmbedded(t *testing.T) {
	if available(fstest.MapFS{"spec/.gitkeep": {}}) {
		t.Error("an empty spec directory reported as available")
	}
	if !available(fstest.MapFS{"spec/sam-openapi.yaml": {Data: []byte("openapi: 3.0.3\n")}}) {
		t.Error("an embedded description reported as unavailable")
	}
}

func TestAMissingDescriptionIsLoggedWithItsCause(t *testing.T) {
	var logged strings.Builder
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	serve(fstest.MapFS{"spec/.gitkeep": {}}, logger).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, Path, nil))

	if !strings.Contains(logged.String(), "OpenAPI description unavailable") || !strings.Contains(logged.String(), "sam-openapi.yaml") {
		t.Errorf("the read error was not logged: %q", logged.String())
	}
}

func TestOnlyThePublicDescriptionIsEmbedded(t *testing.T) {
	entries, err := fs.ReadDir(embedded, specDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != FileName && entry.Name() != ".gitkeep" {
			t.Errorf("%s would ship in every binary; only %s may be embedded", entry.Name(), FileName)
		}
	}
}
