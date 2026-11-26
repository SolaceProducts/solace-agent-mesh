// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const pullTestSpec = "openapi: 3.0.0\npaths: {}\n"

func pullConnectorMock(t *testing.T, conn wire.ConnectorResponse, resourceCalls *atomic.Int32) *reconcile.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/connectors", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ConnectorResponse{conn})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/connectors/{id}/resource", func(w http.ResponseWriter, _ *http.Request) {
		resourceCalls.Add(1)
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "spec.yaml"))
		_, _ = w.Write([]byte(pullTestSpec))
	})
	return newV2PlatformClient(t, mux)
}

func TestPullConnectors_ManagedSpecRoundTrip(t *testing.T) {
	for _, storedPath := range []string{"/api/v1/platform/connectors/id-1/resource", entplatform.SpecResourcePath("id-1")} {
		t.Run(storedPath, func(t *testing.T) {
			var resourceCalls atomic.Int32
			conn := wire.ConnectorResponse{
				ID:               "id-1",
				Name:             "petstore",
				Description:      new("spec-managed connector"),
				ConnectorType:    "api",
				ConnectorSubtype: "openapi",
				HasResource:      true,
				ContentHash:      new("abc123"),
				Values: map[string]any{
					"specification_url": storedPath,
					"file_s3_key":       "<REDACTED>",
					"base_url":          "https://api.example.com",
					"auth_type":         "none",
				},
			}
			c := pullConnectorMock(t, conn, &resourceCalls)

			res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
			entries, names, err := pullConnectors(t.Context(), c, Options{}, res)
			if err != nil {
				t.Fatalf("pullConnectors: %v", err)
			}
			if len(names) != 1 || names[0] != "petstore" {
				t.Errorf("names = %+v", names)
			}
			if len(entries) != 2 {
				t.Fatalf("entries = %d, want sidecar + yaml", len(entries))
			}

			var sidecar, doc *fileEntry
			for i := range entries {
				switch entries[i].relPath {
				case "connectors/petstore.openapi.yaml":
					sidecar = &entries[i]
				case "connectors/petstore.yaml":
					doc = &entries[i]
				}
			}
			if sidecar == nil || doc == nil {
				t.Fatalf("unexpected relPaths: %v, %v", entries[0].relPath, entries[1].relPath)
			}
			if string(sidecar.content) != pullTestSpec {
				t.Errorf("sidecar content = %q", sidecar.content)
			}

			var parsed struct {
				Spec struct {
					Values map[string]any `yaml:"values"`
				} `yaml:"spec"`
			}
			if err := yaml.Unmarshal(doc.content, &parsed); err != nil {
				t.Fatalf("parse emitted YAML: %v\n%s", err, doc.content)
			}
			values := parsed.Spec.Values
			if values["specification_file"] != "petstore.openapi.yaml" {
				t.Errorf("specification_file = %v", values["specification_file"])
			}
			if _, has := values["specification_url"]; has {
				t.Errorf("specification_url survived the rewrite: %v", values["specification_url"])
			}
			if _, has := values["file_s3_key"]; has {
				t.Errorf("legacy file_s3_key survived the rewrite: %v", values["file_s3_key"])
			}
			if resourceCalls.Load() != 1 {
				t.Errorf("resource downloads = %d, want 1", resourceCalls.Load())
			}
		})
	}
}

func TestPullConnectors_DownloadFailureIsLossy(t *testing.T) {
	conn := wire.ConnectorResponse{
		ID:               "id-3",
		Name:             "broken",
		Description:      new("download-failure connector"),
		ConnectorType:    "api",
		ConnectorSubtype: "openapi",
		HasResource:      true,
		Values: map[string]any{
			"specification_url": entplatform.SpecResourcePath("id-3"),
			"base_url":          "https://api.example.com",
			"auth_type":         "none",
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/connectors", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ConnectorResponse{conn})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/connectors/{id}/resource", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusInternalServerError, "boom")
	})
	c := newV2PlatformClient(t, mux)

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullConnectors(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullConnectors: %v", err)
	}
	if len(names) != 1 || len(entries) != 1 {
		t.Fatalf("names = %v entries = %d, want one connector", names, len(entries))
	}
	if len(res.lossy) != 1 || !strings.Contains(res.lossy[0], "spec download failed") {
		t.Errorf("lossy = %v, want one spec-download entry", res.lossy)
	}
}

func TestPullConnectors_URLModeLeftVerbatim(t *testing.T) {
	var resourceCalls atomic.Int32
	conn := wire.ConnectorResponse{
		ID:               "id-2",
		Name:             "livespec",
		Description:      new("url-mode connector"),
		ConnectorType:    "api",
		ConnectorSubtype: "openapi",
		HasResource:      true,
		Values: map[string]any{
			"specification_url": "https://specs.example.com/openapi.yaml",
			"base_url":          "https://api.example.com",
			"auth_type":         "none",
		},
	}
	c := pullConnectorMock(t, conn, &resourceCalls)

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullConnectors(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullConnectors: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want just the connector yaml", len(entries))
	}
	if resourceCalls.Load() != 0 {
		t.Errorf("resource downloads = %d, want 0 for URL-mode", resourceCalls.Load())
	}
	if !strings.Contains(string(entries[0].content), "https://specs.example.com/openapi.yaml") {
		t.Errorf("emitted YAML lost the authored URL:\n%s", entries[0].content)
	}
	if strings.Contains(string(entries[0].content), "specification_file") {
		t.Errorf("URL-mode connector gained a specification_file:\n%s", entries[0].content)
	}
}
