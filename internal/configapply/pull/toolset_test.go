// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"archive/zip"
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func minimalZipBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("manifest.yaml")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := f.Write([]byte("name: openai\nversion: 1.0.0\n")); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func pullToolsetMock(t *testing.T) (*reconcile.Client, []byte) {
	t.Helper()
	zipBytes := minimalZipBytes(t)
	mux := toolsetsMux(uploadedToolset("id-1", "openai", "OpenAI tools"))
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-1/config", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, wire.ToolsetConfiguration{
			Values: map[string]any{
				"api_base": "https://api.openai.com/v1",
				"api_key":  "<REDACTED>",
			},
		})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-1/bundle", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipBytes)
	})
	c := newV2PlatformClient(t, mux)
	return c, zipBytes
}

func TestPullToolsets_EmitsManifestConfigAndBundle(t *testing.T) {
	c, zipBytes := pullToolsetMock(t)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullToolsets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullToolsets: %v", err)
	}
	if len(names) != 1 || names[0] != "openai" {
		t.Fatalf("names = %+v; want [openai]", names)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (yaml + zip), got %d", len(entries))
	}

	wantYAMLPath := filepath.Join("toolsets", "openai.yaml")
	if entries[0].relPath != wantYAMLPath {
		t.Errorf("entries[0].relPath = %q; want %q", entries[0].relPath, wantYAMLPath)
	}
	yaml := string(entries[0].content)
	for _, expect := range []string{
		"kind: toolset",
		"name: openai",
		"config:",
		"api_base: https://api.openai.com/v1",
		"api_key: ${TOOLSET_OPENAI_API_KEY}",
	} {
		if !strings.Contains(yaml, expect) {
			t.Errorf("manifest missing %q:\n%s", expect, yaml)
		}
	}

	wantZipPath := filepath.Join("toolsets", "openai", "openai.zip")
	if entries[1].relPath != wantZipPath {
		t.Errorf("entries[1].relPath = %q; want %q", entries[1].relPath, wantZipPath)
	}
	if !bytes.Equal(entries[1].content, zipBytes) {
		t.Errorf("zip content mismatch: got %d bytes, want %d", len(entries[1].content), len(zipBytes))
	}

	if len(res.SecretPlaceholders) != 1 {
		t.Fatalf("expected 1 secret placeholder warning, got %d (%+v)", len(res.SecretPlaceholders), res.SecretPlaceholders)
	}
	w := res.SecretPlaceholders[0]
	if w.Kind != "toolset" || w.ResourceName != "openai" || w.Placeholder != "${TOOLSET_OPENAI_API_KEY}" {
		t.Errorf("unexpected placeholder: %+v", w)
	}
}

func TestPullToolsets_NameFilter(t *testing.T) {
	c, _ := pullToolsetMock(t)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullToolsets(t.Context(), c, Options{Name: "not-the-toolset"}, res)
	if err != nil {
		t.Fatalf("pullToolsets: %v", err)
	}
	if len(entries) != 0 || len(names) != 0 {
		t.Errorf("expected name filter to drop everything; got entries=%d names=%v", len(entries), names)
	}
}

func TestPullToolsets_ConfigFetchErrorIsLossy(t *testing.T) {
	mux := toolsetsMux(uploadedToolset("id-3", "unready", "no config yet"))
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-3/config", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusInternalServerError, "not ready")
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-3/bundle", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(minimalZipBytes(t))
	})
	c := newV2PlatformClient(t, mux)

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullToolsets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullToolsets: %v", err)
	}
	if len(names) != 1 || len(entries) == 0 {
		t.Fatalf("names = %v entries = %d, want one toolset", names, len(entries))
	}
	assertLossy(t, res, "config fetch failed")
}

func uploadedToolset(id, name, description string) wire.ToolsetResponse {
	return wire.ToolsetResponse{
		ID: id, Name: name, Description: &description, DiscoveryStatus: new(toolsetDiscoveryReady),
		Bundle: &apiv2.ToolsetBundle{Filename: name + ".zip"},
	}
}

func toolsetsMux(toolsets ...wire.ToolsetResponse) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, toolsets)
	})
	return mux
}

func TestPullToolsets_UnservableStateOnlyWarns(t *testing.T) {
	notReady := uploadedToolset("id-6", "pending", "")
	notReady.DiscoveryStatus = new("pending")
	noBundle := uploadedToolset("id-7", "unbundled", "")
	noBundle.Bundle = nil

	for _, tc := range []struct {
		toolset     wire.ToolsetResponse
		wantWarning string
		wantEntries int
	}{
		{notReady, "discovery is pending", 2},
		{noBundle, "no bundle uploaded", 1},
	} {
		t.Run(tc.toolset.Name, func(t *testing.T) {
			mux := toolsetsMux(tc.toolset)
			if *tc.toolset.DiscoveryStatus == toolsetDiscoveryReady {
				mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/"+tc.toolset.ID+"/config", func(w http.ResponseWriter, _ *http.Request) {
					writeV2Single(w, wire.ToolsetConfiguration{Values: map[string]any{}})
				})
			}
			if tc.toolset.Bundle != nil {
				mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/"+tc.toolset.ID+"/bundle", func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write(minimalZipBytes(t))
				})
			}

			res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
			entries, _, err := pullToolsets(t.Context(), newV2PlatformClient(t, mux), Options{}, res)
			if err != nil {
				t.Fatalf("pullToolsets: %v", err)
			}
			if len(res.lossy) != 0 {
				t.Errorf("lossy = %v, want none", res.lossy)
			}
			if len(entries) != tc.wantEntries {
				t.Errorf("entries = %d, want %d", len(entries), tc.wantEntries)
			}
			if !strings.Contains(strings.Join(res.Warnings, "\n"), tc.wantWarning) {
				t.Errorf("warnings = %v, want one containing %q", res.Warnings, tc.wantWarning)
			}
		})
	}
}

func assertLossy(t *testing.T, res *Result, want string) {
	t.Helper()
	for _, msg := range res.lossy {
		if strings.Contains(msg, want) {
			return
		}
	}
	t.Errorf("lossy = %v, want an entry containing %q", res.lossy, want)
}

func TestPullToolsets_DownloadFailureIsLossy(t *testing.T) {
	mux := toolsetsMux(uploadedToolset("id-4", "broken-bundle", ""))
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-4/config", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, wire.ToolsetConfiguration{Values: map[string]any{}})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-4/bundle", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusServiceUnavailable, "bundle storage offline")
	})
	c := newV2PlatformClient(t, mux)

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullToolsets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullToolsets: %v", err)
	}
	if len(names) != 1 || len(entries) != 1 {
		t.Fatalf("names = %v entries = %d, want the toolset with no zip", names, len(entries))
	}
	assertLossy(t, res, "bundle download failed")
}

func TestPullToolsets_EmptyBundleIsLossy(t *testing.T) {
	mux := toolsetsMux(uploadedToolset("id-5", "empty-bundle", ""))
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-5/config", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, wire.ToolsetConfiguration{Values: map[string]any{}})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-5/bundle", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
	})
	c := newV2PlatformClient(t, mux)

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullToolsets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullToolsets: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected yaml only, got %d entries", len(entries))
	}
	assertLossy(t, res, "bundle is empty")
}

func TestPullToolsets_OmitsSpecConfigWhenEmpty(t *testing.T) {
	mux := toolsetsMux(uploadedToolset("id-2", "bare", ""))
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-2/config", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, wire.ToolsetConfiguration{Values: map[string]any{}})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets/id-2/bundle", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(minimalZipBytes(t))
	})
	c := newV2PlatformClient(t, mux)

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullToolsets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullToolsets: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (yaml + zip), got %d", len(entries))
	}
	if strings.Contains(string(entries[0].content), "config:") {
		t.Errorf("expected no spec.config block for bare toolset, got:\n%s", entries[0].content)
	}
}
