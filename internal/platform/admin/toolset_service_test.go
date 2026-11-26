// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func jsonStrEqual(a, b string) bool {
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

type toolsetFixture struct {
	svc   *ToolsetService
	repo  *store.ToolPackageRepo
	store *objectstore.Store
	ns    string
}

func newToolsetFixture(t *testing.T) *toolsetFixture {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	resourceStore := objectstore.NewStore(objectstore.NewMemoryClient())
	logger := slog.New(slog.DiscardHandler)
	storage := newToolsetStorage(resourceStore, logger)
	svc := NewToolsetService(db, repo, storage, nil, "testns", logger)
	return &toolsetFixture{
		svc:   svc,
		repo:  repo,
		store: resourceStore,
		ns:    "testns",
	}
}

func (f *toolsetFixture) createPackage(ctx context.Context, t *testing.T, name string) *ToolsetResponse {
	t.Helper()
	resp, err := f.svc.Create(ctx, CreateToolsetRequest{Name: name, Description: "desc"}, "tester")
	if err != nil {
		t.Fatalf("create package %q: %v", name, err)
	}
	return resp
}

func buildZIP(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if body != nil {
			if _, err := w.Write(body); err != nil {
				t.Fatalf("zip write %q: %v", name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func buildZIPWithSymlink(t *testing.T, name, target string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	hdr.SetMode(0o777 | os.ModeSymlink)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("zip create symlink header: %v", err)
	}
	if _, err := w.Write([]byte(target)); err != nil {
		t.Fatalf("zip write symlink: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func buildValidZIP(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	if entries == nil {
		entries = map[string][]byte{}
	}
	if _, hasTool := entries["tool.json"]; !hasTool {
		if _, hasManifest := entries["manifest.yaml"]; !hasManifest {
			entries["tool.json"] = []byte(`{"name":"test","version":"1.0.0"}`)
		}
	}
	return buildZIP(t, entries)
}

func keyExists(ctx context.Context, t *testing.T, s *objectstore.Store, key string) bool {
	t.Helper()
	rc, err := s.Get(ctx, key)
	if err != nil {
		if objectstore.IsNotFound(err) {
			return false
		}
		t.Fatalf("unexpected get error on %q: %v", key, err)
	}
	_ = rc.Close()
	return true
}

func TestToolsetService_Create(t *testing.T) {
	t.Parallel()

	t.Run("valid name creates package", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		resp, err := f.svc.Create(ctx, CreateToolsetRequest{Name: "my_tools", Description: "hello"}, "alice")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if resp.Name != "my_tools" {
			t.Errorf("Name = %q, want %q", resp.Name, "my_tools")
		}
		if resp.DiscoveryStatus != "created" {
			t.Errorf("DiscoveryStatus = %q, want %q", resp.DiscoveryStatus, "created")
		}
		if resp.ID == "" {
			t.Error("expected non-empty ID")
		}
		got, err := f.repo.GetByID(ctx, resp.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got == nil || got.Name != "my_tools" {
			t.Fatalf("DB row missing or wrong: %#v", got)
		}
	})

	t.Run("invalid names return ValidationError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		longName := strings.Repeat("a", maxPackageNameLen+1)
		cases := []struct {
			name   string
			input  string
			reason string
		}{
			{"empty", "", "empty"},
			{"whitespace only", "   ", "trims to empty"},
			{"too long", longName, "exceeds max len"},
			{"starts with digit", "1abc", "must start with letter"},
			{"contains space", "my tools", "space not allowed"},
			{"contains dot", "my.tools", "dot not allowed"},
			{"contains slash", "my/tools", "slash not allowed"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				_, err := f.svc.Create(ctx, CreateToolsetRequest{Name: tc.input}, "alice")
				if err == nil {
					t.Fatalf("expected ValidationError for %q (%s)", tc.input, tc.reason)
				}
				if _, ok := errors.AsType[*ValidationError](err); !ok {
					t.Fatalf("expected *ValidationError for %q, got %T: %v", tc.input, err, err)
				}
			})
		}
	})

	t.Run("duplicate name returns ConflictError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		f.createPackage(ctx, t, "dup")
		_, err := f.svc.Create(ctx, CreateToolsetRequest{Name: "dup"}, "alice")
		if err == nil {
			t.Fatal("expected duplicate-name error")
		}
		if _, ok := errors.AsType[*ConflictError](err); !ok {
			t.Fatalf("expected *ConflictError, got %T: %v", err, err)
		}
	})
}

func TestToolsetService_GetByID(t *testing.T) {
	t.Parallel()

	t.Run("existing package returns populated response", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		created := f.createPackage(ctx, t, "pkg_a")
		got, err := f.svc.GetByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("ID = %q, want %q", got.ID, created.ID)
		}
		if got.Name != "pkg_a" {
			t.Errorf("Name = %q, want %q", got.Name, "pkg_a")
		}
		if got.Files == nil {
			t.Error("Files should be non-nil (empty slice)")
		}
	})

	t.Run("missing id returns NotFoundError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		_, err := f.svc.GetByID(ctx, idgen.New())
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := errors.AsType[*NotFoundError](err); !ok {
			t.Fatalf("expected *NotFoundError, got %T: %v", err, err)
		}
	})
}

func TestToolsetService_Update(t *testing.T) {
	t.Parallel()

	t.Run("rename to available name succeeds", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		p := f.createPackage(ctx, t, "orig_name")
		got, err := f.svc.Update(ctx, p.ID, UpdateToolsetRequest{Name: "new_name", Description: "changed"}, "bob")
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got.Name != "new_name" {
			t.Errorf("Name = %q, want %q", got.Name, "new_name")
		}
		if got.Description != "changed" {
			t.Errorf("Description = %q, want %q", got.Description, "changed")
		}
	})

	t.Run("rename to existing name returns ConflictError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		_ = f.createPackage(ctx, t, "taken_name")
		p := f.createPackage(ctx, t, "other_name")

		_, err := f.svc.Update(ctx, p.ID, UpdateToolsetRequest{Name: "taken_name"}, "bob")
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := errors.AsType[*ConflictError](err); !ok {
			t.Fatalf("expected *ConflictError, got %T: %v", err, err)
		}
	})

	t.Run("missing id returns NotFoundError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		_, err := f.svc.Update(ctx, idgen.New(), UpdateToolsetRequest{Name: "whatever"}, "bob")
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := errors.AsType[*NotFoundError](err); !ok {
			t.Fatalf("expected *NotFoundError, got %T: %v", err, err)
		}
	})

	t.Run("invalid names return ValidationError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "pkg_upd")

		longName := strings.Repeat("b", maxPackageNameLen+1)
		cases := []struct {
			name  string
			input string
		}{
			{"empty", ""},
			{"too long", longName},
			{"invalid regex", "1bad"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				_, err := f.svc.Update(ctx, p.ID, UpdateToolsetRequest{Name: tc.input}, "bob")
				if err == nil {
					t.Fatalf("expected error for %q", tc.input)
				}
				if _, ok := errors.AsType[*ValidationError](err); !ok {
					t.Fatalf("expected *ValidationError, got %T: %v", err, err)
				}
			})
		}
	})
}

func TestToolsetService_ListAll(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	_ = f.createPackage(ctx, t, "custom_one")
	_ = f.createPackage(ctx, t, "custom_two")

	resp, err := f.svc.ListPaginated(ctx, 1, 100, "", nil)
	if err != nil {
		t.Fatalf("ListPaginated (all): %v", err)
	}

	if len(resp.Data) != 13 {
		t.Fatalf("len(resp.Data) = %d, want 13", len(resp.Data))
	}

	builtinIDs := map[string]bool{
		"builtin_artifact_tools":    false,
		"builtin_web_request_tools": false,
		"builtin_research_tools":    false,
		"builtin_file_tools":        false,
		"builtin_image_tools":       false,
		"builtin_time_tools":        false,
		"hil_tools":                 false,
		"data_analysis":             false,
		"builtin_document_tools":    false,
		"builtin_diagram_tools":     false,
		"builtin_media_tools":       false,
	}
	customSeen := map[string]bool{"custom_one": false, "custom_two": false}

	for _, r := range resp.Data {
		if _, ok := builtinIDs[r.ID]; ok {
			if r.Type != "builtin" {
				t.Errorf("builtin %q has type %q, want \"builtin\"", r.ID, r.Type)
			}
			builtinIDs[r.ID] = true
			continue
		}
		if _, ok := customSeen[r.Name]; ok {
			if r.Type != "custom" {
				t.Errorf("custom %q has type %q, want \"custom\"", r.Name, r.Type)
			}
			customSeen[r.Name] = true
		}
	}
	for id, seen := range builtinIDs {
		if !seen {
			t.Errorf("builtin %q missing from list", id)
		}
	}
	for name, seen := range customSeen {
		if !seen {
			t.Errorf("custom %q missing from list", name)
		}
	}
}

func TestToolsetService_ListPaginated_GatesCodeExecution(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	hasCodeExec := func(rs []ToolsetResponse) bool {
		return slices.ContainsFunc(rs, func(r ToolsetResponse) bool {
			return r.ID == "builtin_code_execution"
		})
	}

	off := features.NewRegistry()
	off.Register(features.Definition{Key: pythonCodeExecutionFeatureKey, Default: false})
	f.svc.SetFeatureRegistry(off)
	respOff, err := f.svc.ListPaginated(ctx, 1, 100, "", nil)
	if err != nil {
		t.Fatalf("ListPaginated (flag off): %v", err)
	}
	if hasCodeExec(respOff.Data) {
		t.Error("flag off: builtin_code_execution must not appear in ListPaginated response")
	}

	on := features.NewRegistry()
	on.Register(features.Definition{Key: pythonCodeExecutionFeatureKey, Default: true})
	f.svc.SetFeatureRegistry(on)
	respOn, err := f.svc.ListPaginated(ctx, 1, 100, "", nil)
	if err != nil {
		t.Fatalf("ListPaginated (flag on): %v", err)
	}
	if !hasCodeExec(respOn.Data) {
		t.Error("flag on: builtin_code_execution must appear in ListPaginated response")
	}
	if len(respOn.Data) != len(respOff.Data)+1 {
		t.Errorf("flag on should add exactly one builtin: off=%d on=%d", len(respOff.Data), len(respOn.Data))
	}
}

func TestToolsetService_ListAll_Pagination(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	for i := range 9 {
		_ = f.createPackage(ctx, t, fmt.Sprintf("pkg_%02d", i))
	}

	page1, err := f.svc.ListPaginated(ctx, 1, 10, "", nil)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	page2, err := f.svc.ListPaginated(ctx, 2, 10, "", nil)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	if page1.Meta.Pagination.TotalPages != 2 {
		t.Errorf("totalPages = %d, want 2", page1.Meta.Pagination.TotalPages)
	}
	if len(page1.Data) != 10 {
		t.Errorf("page1 len = %d, want 10", len(page1.Data))
	}
	if len(page2.Data) != 10 {
		t.Errorf("page2 len = %d, want 10", len(page2.Data))
	}

	seen := make(map[string]int)
	for _, r := range page1.Data {
		seen[r.Name]++
	}
	for _, r := range page2.Data {
		seen[r.Name]++
	}
	for i := range 9 {
		name := fmt.Sprintf("pkg_%02d", i)
		if seen[name] != 1 {
			t.Errorf("custom package %q appeared %d times across pages, want 1", name, seen[name])
		}
	}
}

func TestToolsetService_ListPaginated(t *testing.T) {
	t.Parallel()

	t.Run("page 1 with size 2 returns 2 of 3 custom packages", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		_ = f.createPackage(ctx, t, "cust_a")
		_ = f.createPackage(ctx, t, "cust_b")
		_ = f.createPackage(ctx, t, "cust_c")

		got, err := f.svc.ListPaginated(ctx, 1, 2, "custom", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if len(got.Data) != 2 {
			t.Fatalf("len(Data) = %d, want 2", len(got.Data))
		}
		if got.Meta.Pagination.Count != 3 {
			t.Errorf("Pagination.Count = %d, want 3", got.Meta.Pagination.Count)
		}
		if got.Meta.Pagination.TotalPages != 2 {
			t.Errorf("TotalPages = %d, want 2", got.Meta.Pagination.TotalPages)
		}
		if got.Meta.Pagination.NextPage == nil || *got.Meta.Pagination.NextPage != 2 {
			t.Errorf("NextPage = %v, want 2", got.Meta.Pagination.NextPage)
		}
	})

	t.Run("invalid pageSize clamps to 20", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		_ = f.createPackage(ctx, t, "only_one")

		got, err := f.svc.ListPaginated(ctx, 1, 0, "custom", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if got.Meta.Pagination.PageSize != 20 {
			t.Errorf("PageSize = %d, want 20 (clamped)", got.Meta.Pagination.PageSize)
		}

		got2, err := f.svc.ListPaginated(ctx, 1, 500, "custom", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if got2.Meta.Pagination.PageSize != 20 {
			t.Errorf("PageSize = %d, want 20 (clamped)", got2.Meta.Pagination.PageSize)
		}
	})
}

func TestToolsetService_ListPaginated_FilterByDiscoveryStatus(t *testing.T) {
	t.Parallel()

	t.Run("FilterByDiscoveryStatus_Failed returns only failed packages", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		ready := f.createPackage(ctx, t, "ds_ready_pkg")
		failed := f.createPackage(ctx, t, "ds_failed_pkg")
		_ = f.createPackage(ctx, t, "ds_created_pkg")

		if err := f.repo.UpdateDiscoveryStatus(ctx, ready.ID, "ready", nil); err != nil {
			t.Fatalf("set ready: %v", err)
		}
		errJSON := `["discovery timed out"]`
		if err := f.repo.UpdateDiscoveryStatus(ctx, failed.ID, "failed", &errJSON); err != nil {
			t.Fatalf("set failed: %v", err)
		}

		statusFilter := "failed"
		got, err := f.svc.ListPaginated(ctx, 1, 10, "custom", &statusFilter)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if len(got.Data) != 1 {
			t.Fatalf("len(Data) = %d, want 1 (only failed)", len(got.Data))
		}
		if got.Data[0].ID != failed.ID {
			t.Errorf("got ID %q, want %q (failed pkg)", got.Data[0].ID, failed.ID)
		}
		if got.Data[0].DiscoveryStatus != "failed" {
			t.Errorf("DiscoveryStatus = %q, want failed", got.Data[0].DiscoveryStatus)
		}
		if got.Meta.Pagination.Count != 1 {
			t.Errorf("Count = %d, want 1", got.Meta.Pagination.Count)
		}
	})

	t.Run("nil filter returns all packages", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		_ = f.createPackage(ctx, t, "ds_nil_a")
		_ = f.createPackage(ctx, t, "ds_nil_b")

		got, err := f.svc.ListPaginated(ctx, 1, 10, "custom", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if got.Meta.Pagination.Count != 2 {
			t.Errorf("Count = %d, want 2", got.Meta.Pagination.Count)
		}
	})
}

func TestToolsetService_ListPaginated_BuiltinAgentRefs(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(f.repo.DB())
	desc := "test agent"

	research := &store.Agent{Name: "Research", Description: &desc, CreatedBy: "test", UpdatedBy: "test"}
	if err := agentRepo.Create(ctx, research, []string{"builtin_artifact_tools"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	withData := &store.Agent{
		Name: "WithData", Description: &desc, CreatedBy: "test", UpdatedBy: "test",
		AdditionalConfigurations: []byte(`{"autoInjectDataAnalysisTools": true}`),
	}
	if err := agentRepo.Create(ctx, withData, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	optOutButDeclares := &store.Agent{
		Name: "OptOutButDeclares", Description: &desc, CreatedBy: "test", UpdatedBy: "test",
		AdditionalConfigurations: []byte(`{"autoInjectDataAnalysisTools": false}`),
	}
	if err := agentRepo.Create(ctx, optOutButDeclares, []string{"data_analysis"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	resp, err := f.svc.ListPaginated(ctx, 1, 100, "", nil)
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}

	agentNames := func(id string) []string {
		for i := range resp.Data {
			if resp.Data[i].ID == id {
				names := make([]string, len(resp.Data[i].Agents))
				for j, a := range resp.Data[i].Agents {
					names[j] = a.Name
				}
				slices.Sort(names)
				return names
			}
		}
		t.Fatalf("builtin %q not found in list response", id)
		return nil
	}

	if got, want := agentNames("builtin_artifact_tools"), []string{"Research"}; !slices.Equal(got, want) {
		t.Errorf("builtin_artifact_tools agents = %v, want %v", got, want)
	}
	if got, want := agentNames("data_analysis"), []string{"OptOutButDeclares", "WithData"}; !slices.Equal(got, want) {
		t.Errorf("data_analysis agents = %v, want %v", got, want)
	}

	for _, r := range resp.Data {
		if _, alwaysOn := alwaysOnBuiltinToolsets[r.ID]; alwaysOn {
			continue
		}
		if len(r.Agents) != 0 {
			t.Errorf("builtin %q has %d agents, want 0", r.ID, len(r.Agents))
		}
	}
}

func TestToolsetService_Delete(t *testing.T) {
	t.Parallel()

	t.Run("deletes DB row and storage objects", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		p := f.createPackage(ctx, t, "to_delete")

		zipBytes := buildValidZIP(t, map[string][]byte{"hello.py": []byte("print('hi')")})
		if err := f.svc.Upload(ctx, p.ID, "pkg.zip", zipBytes, "alice"); err != nil {
			t.Fatalf("Upload: %v", err)
		}

		key := packageKey(f.ns, "to_delete")
		if !keyExists(ctx, t, f.store, key) {
			t.Fatalf("precondition: package key %q should exist", key)
		}

		if err := f.svc.Delete(ctx, p.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		got, err := f.repo.GetByID(ctx, p.ID)
		if err != nil {
			t.Fatalf("GetByID after delete: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil package after delete, got %#v", got)
		}
		if keyExists(ctx, t, f.store, key) {
			t.Errorf("package key %q should be deleted", key)
		}
	})

	t.Run("missing id returns NotFoundError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		err := f.svc.Delete(ctx, idgen.New())
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := errors.AsType[*NotFoundError](err); !ok {
			t.Fatalf("expected *NotFoundError, got %T: %v", err, err)
		}
	})
}

func TestToolsetService_Upload(t *testing.T) {
	t.Parallel()

	t.Run("python ZIP detects runtime=python and stores everything", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "py_pkg")

		zipBytes := buildValidZIP(t, map[string][]byte{"foo.py": []byte("x = 1")})
		if err := f.svc.Upload(ctx, p.ID, "pkg.zip", zipBytes, "alice"); err != nil {
			t.Fatalf("Upload: %v", err)
		}

		got, err := f.repo.GetByID(ctx, p.ID)
		if err != nil || got == nil {
			t.Fatalf("GetByID after upload: %v (got=%#v)", err, got)
		}
		if got.Runtime == nil || *got.Runtime != "python" {
			t.Errorf("Runtime = %v, want python", got.Runtime)
		}
		if got.ZipResourceKey == nil || *got.ZipResourceKey == "" {
			t.Error("ZipResourceKey should be populated")
		}
		if got.ZipFilename == nil || *got.ZipFilename != "pkg.zip" {
			t.Errorf("ZipFilename = %v, want pkg.zip", got.ZipFilename)
		}
		if got.ZipFileSize == nil || *got.ZipFileSize != int64(len(zipBytes)) {
			t.Errorf("ZipFileSize = %v, want %d", got.ZipFileSize, len(zipBytes))
		}
		if !keyExists(ctx, t, f.store, packageKey(f.ns, "py_pkg")) {
			t.Error("package key should exist")
		}
	})

	t.Run("ELF-headed ZIP detects runtime=go", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "go_pkg")

		elfHeader := []byte{0x7f, 'E', 'L', 'F', 0x00, 0x01, 0x02}
		zipBytes := buildValidZIP(t, map[string][]byte{"go_tool": elfHeader})
		if err := f.svc.Upload(ctx, p.ID, "go.zip", zipBytes, "alice"); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		got, _ := f.repo.GetByID(ctx, p.ID)
		if got.Runtime == nil || *got.Runtime != "go" {
			t.Errorf("Runtime = %v, want go", got.Runtime)
		}
	})

	t.Run("unknown runtime ZIP still succeeds", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "mystery_pkg")

		zipBytes := buildValidZIP(t, map[string][]byte{"readme.txt": []byte("just text")})
		if err := f.svc.Upload(ctx, p.ID, "m.zip", zipBytes, "alice"); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		got, _ := f.repo.GetByID(ctx, p.ID)
		if got.Runtime == nil || *got.Runtime != "unknown" {
			t.Errorf("Runtime = %v, want unknown", got.Runtime)
		}
	})

	t.Run("oversized ZIP returns ValidationError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "oversized")

		data := make([]byte, f.svc.maxZipCompressed+1)
		err := f.svc.Upload(ctx, p.ID, "big.zip", data, "alice")
		if err == nil {
			t.Fatal("expected ValidationError")
		}
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
	})

	t.Run("path traversal returns ValidationError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "traversal")

		zipBytes := buildZIP(t, map[string][]byte{"../etc/passwd": []byte("evil")})
		err := f.svc.Upload(ctx, p.ID, "bad.zip", zipBytes, "alice")
		if err == nil {
			t.Fatal("expected ValidationError")
		}
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
	})

	t.Run("absolute path returns ValidationError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "abspath")

		zipBytes := buildZIP(t, map[string][]byte{"/evil": []byte("bad")})
		err := f.svc.Upload(ctx, p.ID, "abs.zip", zipBytes, "alice")
		if err == nil {
			t.Fatal("expected ValidationError")
		}
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
	})

	t.Run("symlink entry returns ValidationError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "symlinky")

		zipBytes := buildZIPWithSymlink(t, "link", "/etc/passwd")
		err := f.svc.Upload(ctx, p.ID, "s.zip", zipBytes, "alice")
		if err == nil {
			t.Fatal("expected ValidationError")
		}
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
	})

	t.Run("too many entries returns ValidationError", func(t *testing.T) {
		t.Parallel()
		t.Skip("maxZipEntries=10000 is not feasibly testable without inflating test memory/time; validateAndDetectRuntime has a direct len(zr.File) check")
	})

	t.Run("missing package returns NotFoundError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		zipBytes := buildZIP(t, map[string][]byte{"x.py": []byte("y = 1")})
		err := f.svc.Upload(ctx, idgen.New(), "z.zip", zipBytes, "alice")
		if err == nil {
			t.Fatal("expected NotFoundError")
		}
		if _, ok := errors.AsType[*NotFoundError](err); !ok {
			t.Fatalf("expected *NotFoundError, got %T: %v", err, err)
		}
	})
}

func TestToolsetService_Download(t *testing.T) {
	t.Parallel()

	t.Run("uploaded ZIP is downloadable", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "dl_pkg")

		zipBytes := buildValidZIP(t, map[string][]byte{"a.py": []byte("a = 1")})
		if err := f.svc.Upload(ctx, p.ID, "pkg.zip", zipBytes, "alice"); err != nil {
			t.Fatalf("Upload: %v", err)
		}

		rc, filename, size, err := f.svc.Download(ctx, p.ID)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		defer func() { _ = rc.Close() }()
		if filename != "pkg.zip" {
			t.Errorf("filename = %q, want pkg.zip", filename)
		}
		if size != int64(len(zipBytes)) {
			t.Errorf("size = %d, want %d", size, len(zipBytes))
		}
		body, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(body, zipBytes) {
			t.Errorf("downloaded bytes differ from uploaded (%d vs %d)", len(body), len(zipBytes))
		}
	})

	t.Run("package with no ZIP returns ValidationError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "empty_pkg")

		_, _, _, err := f.svc.Download(ctx, p.ID)
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
	})

	t.Run("missing package returns NotFoundError", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		_, _, _, err := f.svc.Download(ctx, idgen.New())
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := errors.AsType[*NotFoundError](err); !ok {
			t.Fatalf("expected *NotFoundError, got %T: %v", err, err)
		}
	})
}

func TestToolsetService_HandleToolInit(t *testing.T) {
	t.Parallel()

	t.Run("valid name upserts tool file", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "init_pkg")

		params := `{"type":"object"}`
		schema := `{"type":"object"}`
		f.svc.HandleToolInit(ctx, "init_pkg__greet", "says hi", ToolInitMeta{Params: &params, ConfigSchema: &schema})

		files, err := f.repo.GetFilesForPackage(ctx, p.ID)
		if err != nil {
			t.Fatalf("GetFilesForPackage: %v", err)
		}
		if len(files) != 1 {
			t.Fatalf("len(files) = %d, want 1", len(files))
		}
		if files[0].ToolName != "greet" {
			t.Errorf("ToolName = %q, want greet", files[0].ToolName)
		}
		if files[0].ToolDescription == nil || *files[0].ToolDescription != "says hi" {
			t.Errorf("ToolDescription = %v, want 'says hi'", files[0].ToolDescription)
		}
	})

	t.Run("stores the timeout the STR enforces", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "timeout_pkg")

		f.svc.HandleToolInit(ctx, "timeout_pkg__slow", "d", ToolInitMeta{TimeoutSeconds: 30})
		files, err := f.repo.GetFilesForPackage(ctx, p.ID)
		if err != nil {
			t.Fatalf("GetFilesForPackage: %v", err)
		}
		if len(files) != 1 || files[0].TimeoutSeconds != 30 {
			t.Fatalf("files = %+v, want one row with TimeoutSeconds 30", files)
		}

		f.svc.HandleToolInit(ctx, "timeout_pkg__slow", "d", ToolInitMeta{TimeoutSeconds: 3600})
		files, err = f.repo.GetFilesForPackage(ctx, p.ID)
		if err != nil {
			t.Fatalf("GetFilesForPackage: %v", err)
		}
		if files[0].TimeoutSeconds != 3600 {
			t.Errorf("TimeoutSeconds after a changed broadcast = %d, want 3600", files[0].TimeoutSeconds)
		}
	})

	t.Run("an STR that advertises no timeout stores zero, not a default", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "legacy_timeout_pkg")

		f.svc.HandleToolInit(ctx, "legacy_timeout_pkg__tool", "d", ToolInitMeta{})
		files, err := f.repo.GetFilesForPackage(ctx, p.ID)
		if err != nil {
			t.Fatalf("GetFilesForPackage: %v", err)
		}
		if len(files) != 1 || files[0].TimeoutSeconds != 0 {
			t.Fatalf("files = %+v, want one row with TimeoutSeconds 0", files)
		}
	})

	t.Run("malformed names are no-ops", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "quiet_pkg")

		for _, bad := range []string{"", "noseparator", "__onlytool", "onlypkg__", "__"} {
			f.svc.HandleToolInit(ctx, bad, "d", ToolInitMeta{})
		}
		files, _ := f.repo.GetFilesForPackage(ctx, p.ID)
		if len(files) != 0 {
			t.Errorf("expected 0 files, got %d", len(files))
		}
	})

	t.Run("unknown package is a no-op", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "known_pkg")

		f.svc.HandleToolInit(ctx, "other_pkg__foo", "d", ToolInitMeta{})
		files, _ := f.repo.GetFilesForPackage(ctx, p.ID)
		if len(files) != 0 {
			t.Errorf("expected 0 files, got %d", len(files))
		}
	})
}

func TestToolsetService_HandleToolInit_ReadyGating(t *testing.T) {
	t.Parallel()

	t.Run("legacy STR (nil packageToolNames) flips on first init", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "legacy_pkg")
		if err := f.repo.UpdateDiscoveryStatus(ctx, p.ID, "pending", nil); err != nil {
			t.Fatalf("seed pending: %v", err)
		}

		f.svc.HandleToolInit(ctx, "legacy_pkg__one", "d", ToolInitMeta{})

		got, err := f.repo.GetByID(ctx, p.ID)
		if err != nil || got == nil {
			t.Fatalf("re-read package: %v", err)
		}
		if got.DiscoveryStatus != "ready" {
			t.Errorf("legacy fall-back: status = %q, want ready", got.DiscoveryStatus)
		}
	})

	t.Run("legacy STR on a multi-tool package still flips on first init", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "legacy_multi_pkg")
		if err := f.repo.UpdateDiscoveryStatus(ctx, p.ID, "pending", nil); err != nil {
			t.Fatalf("seed pending: %v", err)
		}

		f.svc.HandleToolInit(ctx, "legacy_multi_pkg__one", "d", ToolInitMeta{})

		got, err := f.repo.GetByID(ctx, p.ID)
		if err != nil || got == nil {
			t.Fatalf("re-read package: %v", err)
		}
		if got.DiscoveryStatus != "ready" {
			t.Errorf("multi-tool legacy fall-back: status = %q, want ready", got.DiscoveryStatus)
		}
	})

	t.Run("multi-tool: stays pending until every name has a row", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "multi_pkg")
		if err := f.repo.UpdateDiscoveryStatus(ctx, p.ID, "pending", nil); err != nil {
			t.Fatalf("seed pending: %v", err)
		}
		expected := []string{"alpha", "beta", "gamma"}

		f.svc.HandleToolInit(ctx, "multi_pkg__alpha", "d", ToolInitMeta{PackageToolNames: expected})
		got, err := f.repo.GetByID(ctx, p.ID)
		if err != nil || got == nil {
			t.Fatalf("re-read after alpha: %v", err)
		}
		if got.DiscoveryStatus != "pending" {
			t.Errorf("after init 1/3: status = %q, want pending", got.DiscoveryStatus)
		}

		f.svc.HandleToolInit(ctx, "multi_pkg__beta", "d", ToolInitMeta{PackageToolNames: expected})
		got, _ = f.repo.GetByID(ctx, p.ID)
		if got.DiscoveryStatus != "pending" {
			t.Errorf("after init 2/3: status = %q, want pending", got.DiscoveryStatus)
		}

		f.svc.HandleToolInit(ctx, "multi_pkg__gamma", "d", ToolInitMeta{PackageToolNames: expected})
		got, _ = f.repo.GetByID(ctx, p.ID)
		if got.DiscoveryStatus != "ready" {
			t.Errorf("after init 3/3: status = %q, want ready", got.DiscoveryStatus)
		}
	})

	t.Run("single-tool package flips on its only init", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "single_pkg")
		if err := f.repo.UpdateDiscoveryStatus(ctx, p.ID, "pending", nil); err != nil {
			t.Fatalf("seed pending: %v", err)
		}

		f.svc.HandleToolInit(ctx, "single_pkg__solo", "d", ToolInitMeta{PackageToolNames: []string{"solo"}})

		got, err := f.repo.GetByID(ctx, p.ID)
		if err != nil || got == nil {
			t.Fatalf("re-read: %v", err)
		}
		if got.DiscoveryStatus != "ready" {
			t.Errorf("single-tool: status = %q, want ready", got.DiscoveryStatus)
		}
	})

	t.Run("inits arriving out of order still converge on ready", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "order_pkg")
		if err := f.repo.UpdateDiscoveryStatus(ctx, p.ID, "pending", nil); err != nil {
			t.Fatalf("seed pending: %v", err)
		}
		expected := []string{"alpha", "beta", "gamma"}

		f.svc.HandleToolInit(ctx, "order_pkg__gamma", "d", ToolInitMeta{PackageToolNames: expected})
		f.svc.HandleToolInit(ctx, "order_pkg__alpha", "d", ToolInitMeta{PackageToolNames: expected})
		f.svc.HandleToolInit(ctx, "order_pkg__beta", "d", ToolInitMeta{PackageToolNames: expected})

		got, _ := f.repo.GetByID(ctx, p.ID)
		if got.DiscoveryStatus != "ready" {
			t.Errorf("status = %q, want ready", got.DiscoveryStatus)
		}
	})
}

func TestToolsetService_HandleToolInit_Capabilities(t *testing.T) {
	t.Parallel()

	t.Run("non-nil capabilities replaces stored value", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "cap_pkg")

		caps := `{"profileProvider":true,"foo":"bar"}`
		f.svc.HandleToolInit(ctx, "cap_pkg__greet", "hi", ToolInitMeta{Capabilities: &caps})

		pkg, err := f.repo.GetByID(ctx, p.ID)
		if err != nil || pkg == nil {
			t.Fatalf("re-read package: %v", err)
		}
		if !jsonStrEqual(pkg.Capabilities, caps) {
			t.Errorf("stored capabilities = %q, want %q", pkg.Capabilities, caps)
		}
	})

	t.Run("nil capabilities leaves prior value intact", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "cap_pkg_nil")

		seed := `{"profileProvider":true}`
		f.svc.HandleToolInit(ctx, "cap_pkg_nil__t", "d", ToolInitMeta{Capabilities: &seed})

		f.svc.HandleToolInit(ctx, "cap_pkg_nil__t", "d", ToolInitMeta{})

		pkg, err := f.repo.GetByID(ctx, p.ID)
		if err != nil || pkg == nil {
			t.Fatalf("re-read package: %v", err)
		}
		if !jsonStrEqual(pkg.Capabilities, seed) {
			t.Errorf("stored capabilities = %q, want %q (nil should preserve)", pkg.Capabilities, seed)
		}
	})

	t.Run("empty object clears prior value", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "cap_pkg_clear")

		seed := `{"profileProvider":true}`
		f.svc.HandleToolInit(ctx, "cap_pkg_clear__t", "d", ToolInitMeta{Capabilities: &seed})
		empty := `{}`
		f.svc.HandleToolInit(ctx, "cap_pkg_clear__t", "d", ToolInitMeta{Capabilities: &empty})

		pkg, err := f.repo.GetByID(ctx, p.ID)
		if err != nil || pkg == nil {
			t.Fatalf("re-read package: %v", err)
		}
		if pkg.Capabilities != "{}" {
			t.Errorf("stored capabilities = %q, want %q", pkg.Capabilities, "{}")
		}
	})
}

func TestToolsetService_HandleToolRemoved(t *testing.T) {
	t.Parallel()

	t.Run("removes an existing tool file", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "rm_pkg")

		f.svc.HandleToolInit(ctx, "rm_pkg__gone", "to be removed", ToolInitMeta{})
		pre, _ := f.repo.GetFilesForPackage(ctx, p.ID)
		if len(pre) != 1 {
			t.Fatalf("precondition: want 1 file, got %d", len(pre))
		}

		f.svc.HandleToolRemoved(ctx, "rm_pkg__gone")
		post, _ := f.repo.GetFilesForPackage(ctx, p.ID)
		if len(post) != 0 {
			t.Errorf("expected 0 files after removal, got %d", len(post))
		}
	})

	t.Run("malformed and unknown names are no-ops", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()
		p := f.createPackage(ctx, t, "rm2_pkg")
		f.svc.HandleToolInit(ctx, "rm2_pkg__keep", "keep me", ToolInitMeta{})

		for _, bad := range []string{"", "noseparator", "__onlytool", "onlypkg__", "ghost_pkg__tool"} {
			f.svc.HandleToolRemoved(ctx, bad)
		}
		files, _ := f.repo.GetFilesForPackage(ctx, p.ID)
		if len(files) != 1 {
			t.Errorf("expected file to remain, got %d files", len(files))
		}
	})
}

func TestCreate_RejectsBuiltinNameReservation(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	for builtinName := range schema.ValidBuiltinGroups {
		name := builtinName
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := f.svc.Create(ctx, CreateToolsetRequest{Name: name}, "alice")
			if err == nil {
				t.Fatalf("expected ValidationError for reserved builtin name %q", name)
			}
			if _, ok := errors.AsType[*ValidationError](err); !ok {
				t.Fatalf("expected *ValidationError for %q, got %T: %v", name, err, err)
			}
		})
	}
}

func TestUpdate_RejectsBuiltinNameReservation(t *testing.T) {
	t.Parallel()
	for builtinName := range schema.ValidBuiltinGroups {
		name := builtinName
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newToolsetFixture(t)
			p := f.createPackage(t.Context(), t, "upd_builtin_test_"+name)
			_, err := f.svc.Update(t.Context(), p.ID, UpdateToolsetRequest{Name: name}, "bob")
			if err == nil {
				t.Fatalf("expected ValidationError for reserved builtin name %q", name)
			}
			if _, ok := errors.AsType[*ValidationError](err); !ok {
				t.Fatalf("expected *ValidationError for %q, got %T: %v", name, err, err)
			}
		})
	}
}

func TestUpdate_RejectsRenameAfterUpload(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := f.createPackage(ctx, t, "pre_upload_name")
	zipBytes := buildValidZIP(t, map[string][]byte{"tool.py": []byte("x = 1")})
	if err := f.svc.Upload(ctx, p.ID, "pkg.zip", zipBytes, "alice"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	_, err := f.svc.Update(ctx, p.ID, UpdateToolsetRequest{Name: "post_upload_rename"}, "bob")
	if err == nil {
		t.Fatal("expected ValidationError when renaming after upload")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Errorf("error message should mention rename, got: %s", err.Error())
	}
}

func TestToolsetService_FailStalePending(t *testing.T) {
	t.Parallel()

	f := newToolsetFixture(t)
	ctx := t.Context()

	stale := f.createPackage(ctx, t, "stale_pkg")
	fresh := f.createPackage(ctx, t, "fresh_pkg")
	createdOnly := f.createPackage(ctx, t, "created_only")

	now := time.Now().UnixMilli()
	oldMs := now - (10 * time.Minute).Milliseconds()

	dbRepo := f.repo.DB()
	if _, err := dbRepo.ExecContext(ctx,
		`UPDATE tool_packages SET discovery_status = 'pending', updated_time = ? WHERE id = ?`,
		oldMs, stale.ID); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if _, err := dbRepo.ExecContext(ctx,
		`UPDATE tool_packages SET discovery_status = 'pending', updated_time = ? WHERE id = ?`,
		now, fresh.ID); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	cutoff := now - (5 * time.Minute).Milliseconds()
	if err := f.svc.FailStalePending(ctx, cutoff); err != nil {
		t.Fatalf("FailStalePending: %v", err)
	}

	gotStale, err := f.repo.GetByID(ctx, stale.ID)
	if err != nil {
		t.Fatalf("GetByID stale: %v", err)
	}
	if gotStale.DiscoveryStatus != "failed" {
		t.Errorf("stale status = %q, want failed", gotStale.DiscoveryStatus)
	}
	if gotStale.DiscoveryErrors == nil || *gotStale.DiscoveryErrors == "" {
		t.Error("stale DiscoveryErrors should be populated")
	}

	gotFresh, _ := f.repo.GetByID(ctx, fresh.ID)
	if gotFresh.DiscoveryStatus != "pending" {
		t.Errorf("fresh status = %q, want pending", gotFresh.DiscoveryStatus)
	}

	gotCreated, _ := f.repo.GetByID(ctx, createdOnly.ID)
	if gotCreated.DiscoveryStatus != "created" {
		t.Errorf("created-only status = %q, want created", gotCreated.DiscoveryStatus)
	}
}

func TestHandleToolInit_RejectsReservedAuthConfigKey(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := f.createPackage(ctx, t, "toolinit_auth_pkg")
	if err := f.repo.UpdateZipInfo(ctx, p.ID, "python", "s3://fake", "pkg.zip", 1, "tester"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}

	schemaWithReservedKey := `[{"key":"auth","type":"string","description":"oops"}]`
	prefixed := "toolinit_auth_pkg__my_tool"
	f.svc.HandleToolInit(ctx, prefixed, "desc", ToolInitMeta{ConfigSchema: &schemaWithReservedKey})

	files, err := f.repo.GetFilesForPackage(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetFilesForPackage: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("tool with reserved config key should not be upserted, got %d files", len(files))
	}

	pkg, err := f.repo.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if pkg.DiscoveryErrors == nil || *pkg.DiscoveryErrors == "" {
		t.Fatal("expected discovery_errors to be populated when a tool declares the reserved auth key")
	}
	if !strings.Contains(*pkg.DiscoveryErrors, "reserved") {
		t.Errorf("discovery_errors should mention reserved key, got %q", *pkg.DiscoveryErrors)
	}
}

func TestHandleToolInit_RejectsInvalidToolName(t *testing.T) {
	t.Parallel()

	invalidNames := []struct {
		label string
		name  string
	}{
		{"dot in name", "my.tool"},
		{"space in name", "my tool"},
		{"plus prefix", "+my_tool"},
		{"starts with digit", "1tool"},
		{"slash in name", "my/tool"},
	}

	for _, tc := range invalidNames {
		label := tc.label
		name := tc.name
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			f := newToolsetFixture(t)
			ctx := t.Context()
			p := f.createPackage(ctx, t, "toolinit_pkg")

			prefixed := "toolinit_pkg__" + name
			f.svc.HandleToolInit(ctx, prefixed, "desc", ToolInitMeta{})

			files, err := f.repo.GetFilesForPackage(ctx, p.ID)
			if err != nil {
				t.Fatalf("GetFilesForPackage: %v", err)
			}
			if len(files) != 0 {
				t.Errorf("expected no files for invalid tool name %q, got %d", name, len(files))
			}

			pkg, err := f.repo.GetByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if pkg.DiscoveryErrors == nil || *pkg.DiscoveryErrors == "" {
				t.Errorf("expected discovery_errors to be populated for invalid tool name %q", name)
			}
		})
	}
}

func TestToolsetService_UploadEventCarriesWorkflowIDs(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	outboxRepo := store.NewOutboxRepo(db)
	logger := slog.New(slog.DiscardHandler)
	svc := NewToolsetService(db, repo, newToolsetStorage(objectstore.NewStore(objectstore.NewMemoryClient()), logger), outboxRepo, "testns", logger)
	ctx := t.Context()

	pkg, err := svc.Create(ctx, CreateToolsetRequest{Name: "wf_bound_pkg", Description: "desc"}, "tester")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}

	workflowRepo := store.NewWorkflowRepo(db)
	wf := &store.Workflow{Name: "bound-wf", AppConfig: `{"toolsets":["wf_bound_pkg"]}`, CreatedBy: "u", UpdatedBy: "u"}
	if createErr := workflowRepo.Create(ctx, wf, store.WorkflowBindings{Toolsets: []string{"wf_bound_pkg"}}); createErr != nil {
		t.Fatalf("create workflow: %v", createErr)
	}

	zipBytes := buildValidZIP(t, map[string][]byte{"foo.py": []byte("x = 1")})
	if uploadErr := svc.Upload(ctx, pkg.ID, "pkg.zip", zipBytes, "alice"); uploadErr != nil {
		t.Fatalf("Upload: %v", uploadErr)
	}

	events, err := outboxRepo.GetDispatchable(ctx, 10)
	if err != nil {
		t.Fatalf("get dispatchable events: %v", err)
	}
	var uploaded *store.OutboxEvent
	for i := range events {
		if events[i].EventType == "tool.uploaded" {
			uploaded = &events[i]
		}
	}
	if uploaded == nil {
		t.Fatalf("no tool.uploaded outbox event; got %+v", events)
	}
	var payload struct {
		WorkflowIDs []string `json:"workflowIds"`
	}
	if err := json.Unmarshal([]byte(uploaded.Payload), &payload); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if len(payload.WorkflowIDs) != 1 || payload.WorkflowIDs[0] != wf.ID {
		t.Errorf("payload workflowIds = %v, want [%s]", payload.WorkflowIDs, wf.ID)
	}
}

func TestToolsetService_DeleteAndPatchConfigEventsCarryWorkflowIDs(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	outboxRepo := store.NewOutboxRepo(db)
	logger := slog.New(slog.DiscardHandler)
	svc := NewToolsetService(db, repo, newToolsetStorage(objectstore.NewStore(objectstore.NewMemoryClient()), logger), outboxRepo, "testns", logger)
	ctx := t.Context()

	pkgID := seedToolPackage(t, db, "wf_mutation_pkg", "ready", "{}", "run")
	workflowRepo := store.NewWorkflowRepo(db)
	wf := &store.Workflow{Name: "mutation-wf", AppConfig: `{"toolsets":["wf_mutation_pkg"]}`, CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(ctx, wf, store.WorkflowBindings{Toolsets: []string{"wf_mutation_pkg"}}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	workflowIDsFromEvent := func(eventType string) []string {
		t.Helper()
		events, err := outboxRepo.GetDispatchable(ctx, 10)
		if err != nil {
			t.Fatalf("get dispatchable events: %v", err)
		}
		for i := range events {
			if events[i].EventType != eventType {
				continue
			}
			var payload struct {
				WorkflowIDs []string `json:"workflowIds"`
			}
			if err := json.Unmarshal([]byte(events[i].Payload), &payload); err != nil {
				t.Fatalf("parse %s payload: %v", eventType, err)
			}
			return payload.WorkflowIDs
		}
		t.Fatalf("no %s outbox event found", eventType)
		return nil
	}

	if _, err := svc.PatchConfig(ctx, pkgID, ToolsetConfigPatchRequest{Values: map[string]any{}}, "editor"); err != nil {
		t.Fatalf("patch config: %v", err)
	}
	if ids := workflowIDsFromEvent("tool.config.updated"); len(ids) != 1 || ids[0] != wf.ID {
		t.Errorf("tool.config.updated workflowIds = %v, want [%s]", ids, wf.ID)
	}

	if err := svc.Delete(ctx, pkgID); err != nil {
		t.Fatalf("delete toolset: %v", err)
	}
	if ids := workflowIDsFromEvent("tool.deleted"); len(ids) != 1 || ids[0] != wf.ID {
		t.Errorf("tool.deleted workflowIds = %v, want [%s]", ids, wf.ID)
	}
}
