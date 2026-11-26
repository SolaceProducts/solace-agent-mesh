// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newOpenAPIConnectorFixture(t *testing.T) (*ConnectorService, *store.ConnectorRepo, *ConnectorResponse) {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)
	svc := NewConnectorService(repo, objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns", discardLogger())

	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "petstore",
		Description: "OpenAPI connector for the spec-file tests",
		Type:        "api",
		Subtype:     "openapi",
		Values: map[string]any{
			"base_url":  "https://api.example.com",
			"auth_type": "none",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	return svc, repo, created
}

func TestConnectorService_UploadResource_PersistsContentHash(t *testing.T) {
	t.Parallel()
	svc, repo, created := newOpenAPIConnectorFixture(t)
	ctx := t.Context()

	spec := []byte("openapi: 3.0.0\npaths: {}\n")
	sum := sha256.Sum256(spec)
	want := hex.EncodeToString(sum[:])

	resp, err := svc.UploadResource(ctx, created.ID, "spec.yaml", strings.NewReader(string(spec)))
	if err != nil {
		t.Fatalf("UploadResource: %v", err)
	}
	if resp.ContentHash != want {
		t.Errorf("ConnectorResponse.ContentHash = %q, want %q", resp.ContentHash, want)
	}
	if !resp.HasResource {
		t.Error("HasResource = false after upload, want true")
	}

	row, err := repo.GetByID(ctx, created.ID)
	if err != nil || row == nil {
		t.Fatalf("re-read connector: row=%v err=%v", row, err)
	}
	if row.ContentHash == nil || *row.ContentHash != want {
		t.Errorf("stored content_hash = %v, want %q", row.ContentHash, want)
	}
}

func TestConnectorService_GetByID_NoHashIsEmptyString(t *testing.T) {
	t.Parallel()
	svc, _, created := newOpenAPIConnectorFixture(t)

	resp, err := svc.GetByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resp.ContentHash != "" {
		t.Errorf("ContentHash for never-uploaded connector = %q, want empty", resp.ContentHash)
	}
}

func TestConnectorService_Update_PreservesSpecDisplayPath(t *testing.T) {
	t.Parallel()
	svc, repo, created := newOpenAPIConnectorFixture(t)
	ctx := t.Context()

	if _, err := svc.UploadResource(ctx, created.ID, "spec.yaml", strings.NewReader("openapi: 3.0.0\npaths: {}\n")); err != nil {
		t.Fatalf("UploadResource: %v", err)
	}

	if _, err := svc.Update(ctx, created.ID, UpdateConnectorRequest{
		Values: map[string]any{
			"base_url":  "https://api2.example.com",
			"auth_type": "none",
		},
	}, "user-2"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	row, err := repo.GetByID(ctx, created.ID)
	if err != nil || row == nil {
		t.Fatalf("re-read connector: row=%v err=%v", row, err)
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(*row.Values), &values); err != nil {
		t.Fatalf("parse stored values: %v", err)
	}
	if values["specification_url"] != SpecResourcePath(created.ID) {
		t.Errorf("specification_url after values PATCH = %v, want display path %q", values["specification_url"], SpecResourcePath(created.ID))
	}
	if values["base_url"] != "https://api2.example.com" {
		t.Errorf("base_url = %v, want the patched value", values["base_url"])
	}
}

func TestConnectorService_Update_AcceptsV1DisplayPath(t *testing.T) {
	t.Parallel()
	svc, _, created := newOpenAPIConnectorFixture(t)
	ctx := t.Context()

	const spec = "openapi: 3.0.0\npaths: {}\n"
	if _, err := svc.UploadResource(ctx, created.ID, "spec.yaml", strings.NewReader(spec)); err != nil {
		t.Fatalf("UploadResource: %v", err)
	}

	v1Path := "/api/v1/platform/connectors/" + created.ID + "/resource"
	if _, err := svc.Update(ctx, created.ID, UpdateConnectorRequest{
		Values: map[string]any{
			"base_url":           "https://api.example.com",
			"auth_type":          "none",
			"specification_file": "spec.yaml",
			"specification_url":  v1Path,
		},
	}, "user-2"); err != nil {
		t.Fatalf("Update with v1 display path: %v", err)
	}

	body, _, err := svc.DownloadResource(ctx, created.ID)
	if err != nil {
		t.Fatalf("DownloadResource after update: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	if string(got) != spec {
		t.Errorf("stored spec = %q, want the uploaded spec", got)
	}
}

func TestConnectorService_Update_ExplicitURLWinsOverDisplayPath(t *testing.T) {
	t.Parallel()
	svc, repo, created := newOpenAPIConnectorFixture(t)
	ctx := t.Context()

	if _, err := svc.UploadResource(ctx, created.ID, "spec.yaml", strings.NewReader("openapi: 3.0.0\npaths: {}\n")); err != nil {
		t.Fatalf("UploadResource: %v", err)
	}

	if _, err := svc.Update(ctx, created.ID, UpdateConnectorRequest{
		Values: map[string]any{
			"base_url":          "https://api.example.com",
			"auth_type":         "none",
			"specification_url": "https://specs.example.com/openapi.yaml",
		},
	}, "user-2"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	row, err := repo.GetByID(ctx, created.ID)
	if err != nil || row == nil {
		t.Fatalf("re-read connector: row=%v err=%v", row, err)
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(*row.Values), &values); err != nil {
		t.Fatalf("parse stored values: %v", err)
	}
	if values["specification_url"] != "https://specs.example.com/openapi.yaml" {
		t.Errorf("specification_url = %v, want the explicitly provided URL", values["specification_url"])
	}
}

func TestValidateOpenAPISpecSource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		values  map[string]any
		wantErr bool
	}{
		{
			name:   "file alone is valid",
			values: map[string]any{"specification_file": "petstore.openapi.yaml"},
		},
		{
			name:   "url alone is valid",
			values: map[string]any{"specification_url": "https://x.example.com/spec.yaml"},
		},
		{
			name: "file plus external url conflicts",
			values: map[string]any{
				"specification_file": "petstore.openapi.yaml",
				"specification_url":  "https://x.example.com/spec.yaml",
			},
			wantErr: true,
		},
		{
			name: "file plus legacy s3 key conflicts",
			values: map[string]any{
				"specification_file": "petstore.openapi.yaml",
				"file_s3_key":        "bucket/key",
			},
			wantErr: true,
		},
		{
			name: "file plus platform display path does not conflict",
			values: map[string]any{
				"specification_file": "petstore.openapi.yaml",
				"specification_url":  SpecResourcePath("770e8400-e29b-41d4-a716-446655440000"),
			},
		},
		{
			name: "file plus v1 platform display path an earlier release wrote does not conflict",
			values: map[string]any{
				"specification_file": "petstore.openapi.yaml",
				"specification_url":  "/api/v1/platform/connectors/770e8400-e29b-41d4-a716-446655440000/resource",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateOpenAPISpecSource(tc.values)
			if tc.wantErr && err == nil {
				t.Error("want validation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("want nil, got %v", err)
			}
		})
	}
}

func TestResolveSpecSource(t *testing.T) {
	t.Parallel()

	t.Run("managed resource wins over stale url", func(t *testing.T) {
		t.Parallel()
		conn := apiConnector(map[string]any{
			"specification_url": "https://stale.example.com/spec.yaml",
			"base_url":          "https://api.example.com",
		})
		if got := resolveSpecSource(conn); got != "objstore://"+testAPIConnectorResourceKey {
			t.Errorf("resolveSpecSource = %q, want the objstore reference", got)
		}
	})

	t.Run("external url without resource is emitted verbatim", func(t *testing.T) {
		t.Parallel()
		conn := apiConnector(map[string]any{
			"specification_url": "https://specs.example.com/openapi.yaml",
			"base_url":          "https://api.example.com",
		})
		conn.ResourceKey = ""
		if got := resolveSpecSource(conn); got != "https://specs.example.com/openapi.yaml" {
			t.Errorf("resolveSpecSource = %q, want the live URL", got)
		}
	})

	t.Run("display path without resource is not emitted", func(t *testing.T) {
		t.Parallel()
		conn := apiConnector(map[string]any{
			"specification_url": SpecResourcePath("770e8400-e29b-41d4-a716-446655440000"),
			"base_url":          "https://api.example.com",
		})
		conn.ResourceKey = ""
		if got := resolveSpecSource(conn); got != "ERROR" {
			t.Errorf("resolveSpecSource = %q, want the ERROR sentinel", got)
		}
	})
}
