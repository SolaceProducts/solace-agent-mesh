// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/google/go-cmp/cmp"
)

func newTestPackage(name string) *ToolPackage {
	desc := "Test package for " + name
	return &ToolPackage{
		Name:            name,
		Description:     &desc,
		DiscoveryStatus: "created",
		CreatedBy:       "alice",
		UpdatedBy:       "alice",
	}
}

func TestToolPackageRepo_CreateGet(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	t.Run("roundtrip", func(t *testing.T) {
		pkg := newTestPackage("pkg-roundtrip")
		if err := repo.Create(ctx, pkg); err != nil {
			t.Fatalf("create: %v", err)
		}
		if pkg.ID == "" {
			t.Fatal("expected ID to be assigned")
		}
		if pkg.CreatedTime == 0 || pkg.UpdatedTime == 0 {
			t.Fatal("expected timestamps to be set by Create")
		}

		byID, err := repo.GetByID(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if byID == nil {
			t.Fatal("expected package by ID")
		}
		if byID.Name != pkg.Name {
			t.Errorf("name = %q, want %q", byID.Name, pkg.Name)
		}
		if byID.DiscoveryStatus != "created" {
			t.Errorf("discovery_status = %q, want %q", byID.DiscoveryStatus, "created")
		}
		if byID.Description == nil || *byID.Description != *pkg.Description {
			t.Errorf("description = %v, want %q", byID.Description, *pkg.Description)
		}

		byName, err := repo.GetByName(ctx, pkg.Name)
		if err != nil {
			t.Fatalf("GetByName: %v", err)
		}
		if byName == nil || byName.ID != pkg.ID {
			t.Fatalf("GetByName: got %v, want ID %q", byName, pkg.ID)
		}
	})

	t.Run("get_by_id_unknown_is_nil", func(t *testing.T) {
		got, err := repo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("get_by_id_non_uuid_is_nil", func(t *testing.T) {
		got, err := repo.GetByID(ctx, "not-a-uuid")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("get_by_name_unknown_is_nil", func(t *testing.T) {
		got, err := repo.GetByName(ctx, "does-not-exist")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("create_preserves_supplied_id_and_timestamps", func(t *testing.T) {
		fixedID := "10000000-0000-7000-8000-000000000aaa"
		fixedTime := int64(1_700_000_000_000)
		pkg := newTestPackage("pkg-fixed")
		pkg.ID = fixedID
		pkg.CreatedTime = fixedTime
		pkg.UpdatedTime = fixedTime
		if err := repo.Create(ctx, pkg); err != nil {
			t.Fatalf("create: %v", err)
		}
		got, err := repo.GetByID(ctx, fixedID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got == nil || got.CreatedTime != fixedTime || got.UpdatedTime != fixedTime {
			t.Errorf("timestamps not preserved: %+v", got)
		}
	})
}

func TestToolPackageRepo_UpdatePackage(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("pkg-update")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create: %v", err)
	}
	originalUpdated := pkg.UpdatedTime

	time.Sleep(2 * time.Millisecond)

	newDesc := "updated description"
	if err := repo.UpdatePackage(ctx, pkg.ID, "pkg-update-renamed", &newDesc, "bob"); err != nil {
		t.Fatalf("UpdatePackage: %v", err)
	}

	got, err := repo.GetByID(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "pkg-update-renamed" {
		t.Errorf("name = %q, want %q", got.Name, "pkg-update-renamed")
	}
	if got.Description == nil || *got.Description != newDesc {
		t.Errorf("description = %v, want %q", got.Description, newDesc)
	}
	if got.UpdatedBy != "bob" {
		t.Errorf("updated_by = %q, want %q", got.UpdatedBy, "bob")
	}
	if got.UpdatedTime <= originalUpdated {
		t.Errorf("updated_time should advance: was %d, now %d", originalUpdated, got.UpdatedTime)
	}

	t.Run("unknown_id_returns_error", func(t *testing.T) {
		err := repo.UpdatePackage(ctx, "missing-id", "x", nil, "bob")
		if err == nil {
			t.Fatal("expected error for unknown id")
		}
	})
}

func TestToolPackageRepo_Delete(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	agentRepo := NewAgentRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("pkg-delete")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}

	timeout := 60
	file := &ToolPackageFile{
		PackageID:      pkg.ID,
		ToolName:       "my_tool",
		TimeoutSeconds: timeout,
		SandboxProfile: "standard",
	}
	if err := repo.UpsertToolFile(ctx, file); err != nil {
		t.Fatalf("UpsertToolFile: %v", err)
	}

	agent := &Agent{Name: "agent-for-delete", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}
	cfg := `{"key":"value"}`
	if err := repo.SetToolsetConfigsForAgent(ctx, agent.ID, []AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: pkg.ID, ConfigValues: &cfg},
	}); err != nil {
		t.Fatalf("SetToolsetConfigsForAgent: %v", err)
	}

	files, err := repo.GetFilesForPackage(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetFilesForPackage: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file before delete, got %d", len(files))
	}
	configs, err := repo.GetToolsetConfigsForAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetToolsetConfigsForAgent: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 toolset config before delete, got %d", len(configs))
	}

	if delErr := repo.Delete(ctx, pkg.ID); delErr != nil {
		t.Fatalf("Delete: %v", delErr)
	}

	got, err := repo.GetByID(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil package after delete, got %+v", got)
	}

	files, err = repo.GetFilesForPackage(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetFilesForPackage after delete: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files after cascade delete, got %d", len(files))
	}

	configs, err = repo.GetToolsetConfigsForAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetToolsetConfigsForAgent after delete: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("expected 0 toolset configs after cascade delete, got %d", len(configs))
	}

	t.Run("delete_unknown_returns_error", func(t *testing.T) {
		err := repo.Delete(ctx, "missing-id")
		if err == nil {
			t.Fatal("expected error for unknown id")
		}
	})
}

func TestToolPackageRepo_ListPaginated(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	base := time.Now().UnixMilli()
	for i, name := range []string{"pkg-a", "pkg-b", "pkg-c"} {
		pkg := newTestPackage(name)
		pkg.CreatedTime = base + int64(i)
		pkg.UpdatedTime = base + int64(i)
		if err := repo.Create(ctx, pkg); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	t.Run("page_one_size_two", func(t *testing.T) {
		list, total, err := repo.ListPaginated(ctx, 1, 2, "", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}
		if len(list) != 2 {
			t.Errorf("len = %d, want 2", len(list))
		}
	})

	t.Run("page_two_size_two", func(t *testing.T) {
		list, total, err := repo.ListPaginated(ctx, 2, 2, "", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}
		if len(list) != 1 {
			t.Errorf("len = %d, want 1", len(list))
		}
	})

	t.Run("out_of_range_empty", func(t *testing.T) {
		list, total, err := repo.ListPaginated(ctx, 99, 10, "", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}
		if len(list) != 0 {
			t.Errorf("expected no rows, got %d", len(list))
		}
	})

	t.Run("type_filter_custom_returns_all", func(t *testing.T) {
		list, total, err := repo.ListPaginated(ctx, 1, 10, "custom", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}
		if len(list) != 3 {
			t.Errorf("len = %d, want 3", len(list))
		}
	})
}

func TestToolPackageRepo_ListPaginated_DiscoveryStatusFilter(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	now := time.Now().UnixMilli()

	pkgReady := newTestPackage("filter-ready")
	pkgReady.DiscoveryStatus = "ready"
	pkgReady.CreatedTime = now
	pkgReady.UpdatedTime = now

	pkgFailed1 := newTestPackage("filter-failed-1")
	pkgFailed1.DiscoveryStatus = "failed"
	pkgFailed1.CreatedTime = now + 1
	pkgFailed1.UpdatedTime = now + 1

	pkgFailed2 := newTestPackage("filter-failed-2")
	pkgFailed2.DiscoveryStatus = "failed"
	pkgFailed2.CreatedTime = now + 2
	pkgFailed2.UpdatedTime = now + 2

	pkgCreated := newTestPackage("filter-created")
	pkgCreated.DiscoveryStatus = "created"
	pkgCreated.CreatedTime = now + 3
	pkgCreated.UpdatedTime = now + 3

	for _, p := range []*ToolPackage{pkgReady, pkgFailed1, pkgFailed2, pkgCreated} {
		if err := repo.Create(ctx, p); err != nil {
			t.Fatalf("create %s: %v", p.Name, err)
		}
	}

	t.Run("filter_failed_returns_only_failed", func(t *testing.T) {
		status := "failed"
		list, total, err := repo.ListPaginated(ctx, 1, 10, "", &status)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if total != 2 {
			t.Errorf("total = %d, want 2", total)
		}
		if len(list) != 2 {
			t.Errorf("len = %d, want 2", len(list))
		}
		for _, p := range list {
			if p.DiscoveryStatus != "failed" {
				t.Errorf("unexpected status %q for package %q", p.DiscoveryStatus, p.Name)
			}
		}
	})

	t.Run("filter_ready_returns_one", func(t *testing.T) {
		status := "ready"
		list, total, err := repo.ListPaginated(ctx, 1, 10, "", &status)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1", total)
		}
		if len(list) != 1 || list[0].Name != "filter-ready" {
			t.Errorf("unexpected result: %+v", list)
		}
	})

	t.Run("nil_filter_returns_all", func(t *testing.T) {
		list, total, err := repo.ListPaginated(ctx, 1, 10, "", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if total != 4 {
			t.Errorf("total = %d, want 4", total)
		}
		if len(list) != 4 {
			t.Errorf("len = %d, want 4", len(list))
		}
	})
}

func TestToolPackageRepo_UpdateZipInfo(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("pkg-zip")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create: %v", err)
	}

	preFile := &ToolPackageFile{
		PackageID:      pkg.ID,
		ToolName:       "old_tool",
		SandboxProfile: "standard",
		TimeoutSeconds: 300,
	}
	if err := repo.UpsertToolFile(ctx, preFile); err != nil {
		t.Fatalf("UpsertToolFile: %v", err)
	}

	if err := repo.UpdateZipInfo(ctx, pkg.ID, "python", "tool-packages/abc/package.zip", "pkg.zip", 1234, "carol"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}

	got, err := repo.GetByID(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Runtime == nil || *got.Runtime != "python" {
		t.Errorf("runtime = %v, want python", got.Runtime)
	}
	if got.ZipResourceKey == nil || *got.ZipResourceKey != "tool-packages/abc/package.zip" {
		t.Errorf("zip_resource_key = %v", got.ZipResourceKey)
	}
	if got.ZipFilename == nil || *got.ZipFilename != "pkg.zip" {
		t.Errorf("zip_filename = %v", got.ZipFilename)
	}
	if got.ZipFileSize == nil || *got.ZipFileSize != 1234 {
		t.Errorf("zip_file_size = %v, want 1234", got.ZipFileSize)
	}
	if got.UpdatedBy != "carol" {
		t.Errorf("updated_by = %q, want carol", got.UpdatedBy)
	}
	if got.DiscoveryStatus != "pending" {
		t.Errorf("discovery_status = %q, want pending", got.DiscoveryStatus)
	}

	files, err := repo.GetFilesForPackage(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetFilesForPackage: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files after UpdateZipInfo, got %d", len(files))
	}

	t.Run("unknown_id_returns_error", func(t *testing.T) {
		err := repo.UpdateZipInfo(ctx, "missing", "python", "k", "n", 1, "u")
		if err == nil {
			t.Fatal("expected error for unknown id")
		}
	})
}

func TestToolPackageRepo_UpdateDiscoveryStatus(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("pkg-status")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create: %v", err)
	}

	errJSON := `{"error":"bad zip"}`
	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "failed", &errJSON); err != nil {
		t.Fatalf("UpdateDiscoveryStatus: %v", err)
	}

	got, err := repo.GetByID(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DiscoveryStatus != "failed" {
		t.Errorf("discovery_status = %q, want failed", got.DiscoveryStatus)
	}
	if got.DiscoveryErrors == nil || *got.DiscoveryErrors != errJSON {
		t.Errorf("discovery_errors = %v, want %q", got.DiscoveryErrors, errJSON)
	}

	if updateErr := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); updateErr != nil {
		t.Fatalf("UpdateDiscoveryStatus ready: %v", updateErr)
	}
	got, err = repo.GetByID(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DiscoveryStatus != "ready" {
		t.Errorf("discovery_status = %q, want ready", got.DiscoveryStatus)
	}
	if got.DiscoveryErrors != nil {
		t.Errorf("expected nil discovery_errors, got %v", got.DiscoveryErrors)
	}
}

func TestToolPackageRepo_MarkReadyIfToolsRegistered(t *testing.T) {
	badZip := `{"error":"bad zip"}`
	type discovery struct {
		Status string
		Errors *string
	}
	tests := []struct {
		name       string
		before     discovery
		registered []string
		expected   []string
		want       discovery
	}{
		{
			name:       "every expected tool registered marks ready and clears errors",
			before:     discovery{Status: "failed", Errors: &badZip},
			registered: []string{"echo", "sum"},
			expected:   []string{"echo", "sum"},
			want:       discovery{Status: "ready"},
		},
		{
			name:     "files cleared by an upload leave the package pending",
			before:   discovery{Status: "pending"},
			expected: []string{"echo"},
			want:     discovery{Status: "pending"},
		},
		{
			name:       "one expected tool unregistered leaves the package pending",
			before:     discovery{Status: "pending"},
			registered: []string{"echo"},
			expected:   []string{"echo", "sum"},
			want:       discovery{Status: "pending"},
		},
		{
			name:       "a tool outside the expected list does not count",
			before:     discovery{Status: "pending"},
			registered: []string{"sum"},
			expected:   []string{"echo"},
			want:       discovery{Status: "pending"},
		},
		{
			name:       "a tool expected twice counts once",
			before:     discovery{Status: "pending"},
			registered: []string{"echo"},
			expected:   []string{"echo", "echo"},
			want:       discovery{Status: "ready"},
		},
		{
			name:   "an empty expected list leaves the package pending",
			before: discovery{Status: "pending"},
			want:   discovery{Status: "pending"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewToolPackageRepo(TestDB(t))
			ctx := t.Context()
			pkg := newTestPackage("mark-ready-pkg")
			if err := repo.Create(ctx, pkg); err != nil {
				t.Fatalf("create: %v", err)
			}
			for _, name := range tt.registered {
				if err := repo.UpsertToolFile(ctx, &ToolPackageFile{PackageID: pkg.ID, ToolName: name, SandboxProfile: "standard", TimeoutSeconds: 300}); err != nil {
					t.Fatalf("UpsertToolFile(%q): %v", name, err)
				}
			}
			if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, tt.before.Status, tt.before.Errors); err != nil {
				t.Fatalf("UpdateDiscoveryStatus: %v", err)
			}

			if err := repo.MarkReadyIfToolsRegistered(ctx, pkg.ID, tt.expected); err != nil {
				t.Fatalf("MarkReadyIfToolsRegistered(%v) = %v, want nil", tt.expected, err)
			}

			got, err := repo.GetByID(ctx, pkg.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if diff := cmp.Diff(tt.want, discovery{Status: got.DiscoveryStatus, Errors: got.DiscoveryErrors}); diff != "" {
				t.Errorf("MarkReadyIfToolsRegistered(%v) discovery mismatch (-want +got):\n%s", tt.expected, diff)
			}
		})
	}
}

func TestToolPackageRepo_MarkReadyIfToolsRegistered_UnknownPackageIsNoOp(t *testing.T) {
	const unknownID = "00000000-0000-0000-0000-000000000000"
	repo := NewToolPackageRepo(TestDB(t))
	if err := repo.MarkReadyIfToolsRegistered(t.Context(), unknownID, []string{"echo"}); err != nil {
		t.Errorf("MarkReadyIfToolsRegistered(%q) = %v, want nil", unknownID, err)
	}
}

func TestToolPackageRepo_Capabilities(t *testing.T) {
	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	t.Run("create defaults to empty object", func(t *testing.T) {
		pkg := newTestPackage("caps-default")
		if err := repo.Create(ctx, pkg); err != nil {
			t.Fatalf("create: %v", err)
		}
		got, err := repo.GetByID(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Capabilities != "{}" {
			t.Errorf("default capabilities = %q, want %q", got.Capabilities, "{}")
		}
	})

	t.Run("update round-trip", func(t *testing.T) {
		pkg := newTestPackage("caps-rt")
		if err := repo.Create(ctx, pkg); err != nil {
			t.Fatalf("create: %v", err)
		}
		caps := `{"profileProvider":true,"x":1}`
		if err := repo.UpdateCapabilities(ctx, pkg.ID, caps); err != nil {
			t.Fatalf("UpdateCapabilities: %v", err)
		}
		got, err := repo.GetByID(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		var roundTrip, want map[string]any
		if err := json.Unmarshal([]byte(got.Capabilities), &roundTrip); err != nil {
			t.Fatalf("unmarshal stored capabilities %q: %v", got.Capabilities, err)
		}
		if err := json.Unmarshal([]byte(caps), &want); err != nil {
			t.Fatalf("unmarshal expected: %v", err)
		}
		if len(roundTrip) != len(want) {
			t.Errorf("capabilities len = %d, want %d", len(roundTrip), len(want))
		}
		if roundTrip["profileProvider"] != true {
			t.Errorf("profileProvider = %v, want true", roundTrip["profileProvider"])
		}
	})

	t.Run("empty string normalizes to empty object", func(t *testing.T) {
		pkg := newTestPackage("caps-empty")
		if err := repo.Create(ctx, pkg); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := repo.UpdateCapabilities(ctx, pkg.ID, `{"x":true}`); err != nil {
			t.Fatalf("UpdateCapabilities seed: %v", err)
		}
		if err := repo.UpdateCapabilities(ctx, pkg.ID, ""); err != nil {
			t.Fatalf("UpdateCapabilities clear: %v", err)
		}
		got, err := repo.GetByID(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Capabilities != "{}" {
			t.Errorf("cleared capabilities = %q, want %q", got.Capabilities, "{}")
		}
	})

	t.Run("list paginated surfaces capabilities", func(t *testing.T) {
		pkg := newTestPackage("caps-list")
		if err := repo.Create(ctx, pkg); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := repo.UpdateCapabilities(ctx, pkg.ID, `{"profileProvider":true}`); err != nil {
			t.Fatalf("UpdateCapabilities: %v", err)
		}
		rows, _, err := repo.ListPaginated(ctx, 1, 100, "", nil)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		var hit *ToolPackage
		for i := range rows {
			if rows[i].ID == pkg.ID {
				hit = &rows[i]
				break
			}
		}
		if hit == nil {
			t.Fatalf("created package not in ListPaginated results")
		}
		if hit.Capabilities == "" || hit.Capabilities == "{}" {
			t.Errorf("ListPaginated capabilities = %q, want non-empty JSON object", hit.Capabilities)
		}
	})
}

func TestToolPackageRepo_ListPendingOlderThan(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	now := time.Now().UnixMilli()

	oldPending := newTestPackage("pkg-old-pending")
	oldPending.DiscoveryStatus = "pending"
	oldPending.CreatedTime = now - 60_000
	oldPending.UpdatedTime = now - 60_000
	if err := repo.Create(ctx, oldPending); err != nil {
		t.Fatalf("create old: %v", err)
	}

	freshPending := newTestPackage("pkg-fresh-pending")
	freshPending.DiscoveryStatus = "pending"
	freshPending.CreatedTime = now
	freshPending.UpdatedTime = now
	if err := repo.Create(ctx, freshPending); err != nil {
		t.Fatalf("create fresh: %v", err)
	}

	oldReady := newTestPackage("pkg-old-ready")
	oldReady.DiscoveryStatus = "ready"
	oldReady.CreatedTime = now - 60_000
	oldReady.UpdatedTime = now - 60_000
	if err := repo.Create(ctx, oldReady); err != nil {
		t.Fatalf("create old ready: %v", err)
	}

	cutoff := now - 30_000
	stuck, err := repo.ListPendingOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("ListPendingOlderThan: %v", err)
	}
	if len(stuck) != 1 {
		t.Fatalf("expected 1 stuck package, got %d", len(stuck))
	}
	if stuck[0].ID != oldPending.ID {
		t.Errorf("got %q, want %q (old pending)", stuck[0].ID, oldPending.ID)
	}
}

func TestToolPackageRepo_UpsertToolFile(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("pkg-upsert")
	pkg.DiscoveryStatus = "pending"
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("insert_new_row", func(t *testing.T) {
		desc := "greet the user"
		params := `{"type":"object"}`
		file := &ToolPackageFile{
			PackageID:       pkg.ID,
			ToolName:        "greet",
			ToolDescription: &desc,
			TimeoutSeconds:  120,
			SandboxProfile:  "standard",
			Parameters:      &params,
		}
		if err := repo.UpsertToolFile(ctx, file); err != nil {
			t.Fatalf("UpsertToolFile: %v", err)
		}
		if file.ID == "" {
			t.Fatal("expected ID to be assigned")
		}
		files, err := repo.GetFilesForPackage(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetFilesForPackage: %v", err)
		}
		if len(files) != 1 {
			t.Fatalf("expected 1 file, got %d", len(files))
		}
		if files[0].ToolName != "greet" {
			t.Errorf("tool_name = %q, want greet", files[0].ToolName)
		}

		got, err := repo.GetByID(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.DiscoveryStatus != "pending" {
			t.Errorf("discovery_status = %q, want pending (repo no longer flips status)", got.DiscoveryStatus)
		}
	})

	t.Run("auth_column_round_trips", func(t *testing.T) {
		authJSON := `{"type":"oauth2","scheme":{"authorization_url":"https://idp.example.com/authorize","token_url":"https://idp.example.com/token","scopes":["read"]}}`
		file := &ToolPackageFile{
			PackageID:      pkg.ID,
			ToolName:       "authed_greet",
			TimeoutSeconds: 300,
			SandboxProfile: "standard",
			Auth:           &authJSON,
		}
		if err := repo.UpsertToolFile(ctx, file); err != nil {
			t.Fatalf("UpsertToolFile with auth: %v", err)
		}

		files, err := repo.GetFilesForPackage(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetFilesForPackage: %v", err)
		}
		var got *ToolPackageFile
		for i := range files {
			if files[i].ToolName == "authed_greet" {
				got = &files[i]
				break
			}
		}
		if got == nil {
			t.Fatal("authed_greet file missing")
		}
		if got.Auth == nil {
			t.Fatal("Auth column nil after round-trip")
		}
		if *got.Auth != authJSON {
			t.Errorf("Auth = %q, want %q", *got.Auth, authJSON)
		}

		file.Auth = nil
		if err := repo.UpsertToolFile(ctx, file); err != nil {
			t.Fatalf("UpsertToolFile clearing auth: %v", err)
		}
		files, _ = repo.GetFilesForPackage(ctx, pkg.ID)
		for i := range files {
			if files[i].ToolName == "authed_greet" && files[i].Auth != nil {
				t.Errorf("Auth not cleared after nil upsert; got %q", *files[i].Auth)
			}
		}
	})

	t.Run("upsert_same_key_updates_in_place", func(t *testing.T) {
		newDesc := "updated greeting"
		file := &ToolPackageFile{
			PackageID:       pkg.ID,
			ToolName:        "greet",
			ToolDescription: &newDesc,
			TimeoutSeconds:  600,
			SandboxProfile:  "sandboxed",
		}
		if err := repo.UpsertToolFile(ctx, file); err != nil {
			t.Fatalf("upsert again: %v", err)
		}

		files, err := repo.GetFilesForPackage(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetFilesForPackage: %v", err)
		}
		var got *ToolPackageFile
		for i := range files {
			if files[i].ToolName == "greet" {
				got = &files[i]
				break
			}
		}
		if got == nil {
			t.Fatal("greet row missing after upsert")
		}
		if got.ToolDescription == nil || *got.ToolDescription != newDesc {
			t.Errorf("description = %v, want %q", got.ToolDescription, newDesc)
		}
		if got.TimeoutSeconds != 600 {
			t.Errorf("timeout = %d, want 600", got.TimeoutSeconds)
		}
		if got.SandboxProfile != "sandboxed" {
			t.Errorf("sandbox_profile = %q, want sandboxed", got.SandboxProfile)
		}
	})
}

func TestToolPackageRepo_RemoveToolFile(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("pkg-remove")
	pkg.DiscoveryStatus = "pending"
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, name := range []string{"keep_me", "remove_me"} {
		file := &ToolPackageFile{
			PackageID:      pkg.ID,
			ToolName:       name,
			SandboxProfile: "standard",
			TimeoutSeconds: 300,
		}
		if err := repo.UpsertToolFile(ctx, file); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}
	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatalf("flip to ready: %v", err)
	}

	if err := repo.RemoveToolFile(ctx, pkg.ID, "remove_me"); err != nil {
		t.Fatalf("RemoveToolFile: %v", err)
	}

	files, err := repo.GetFilesForPackage(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetFilesForPackage: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 remaining file, got %d", len(files))
	}
	if files[0].ToolName != "keep_me" {
		t.Errorf("remaining = %q, want keep_me", files[0].ToolName)
	}

	got, err := repo.GetByID(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DiscoveryStatus != "ready" {
		t.Errorf("discovery_status = %q, want ready (files remain)", got.DiscoveryStatus)
	}

	if removeErr := repo.RemoveToolFile(ctx, pkg.ID, "keep_me"); removeErr != nil {
		t.Fatalf("RemoveToolFile (last): %v", removeErr)
	}
	got, err = repo.GetByID(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DiscoveryStatus != "removed" {
		t.Errorf("discovery_status = %q, want removed", got.DiscoveryStatus)
	}
}

func TestToolPackageRepo_GetFilesForPackages(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkgA := newTestPackage("pkg-files-a")
	pkgA.DiscoveryStatus = "pending"
	pkgB := newTestPackage("pkg-files-b")
	pkgB.DiscoveryStatus = "pending"
	if err := repo.Create(ctx, pkgA); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := repo.Create(ctx, pkgB); err != nil {
		t.Fatalf("create b: %v", err)
	}

	for _, n := range []string{"tool_a1", "tool_a2"} {
		if err := repo.UpsertToolFile(ctx, &ToolPackageFile{
			PackageID: pkgA.ID, ToolName: n,
			SandboxProfile: "standard", TimeoutSeconds: 300,
		}); err != nil {
			t.Fatalf("upsert a: %v", err)
		}
	}
	if err := repo.UpsertToolFile(ctx, &ToolPackageFile{
		PackageID: pkgB.ID, ToolName: "tool_b1",
		SandboxProfile: "standard", TimeoutSeconds: 300,
	}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}

	t.Run("single_package", func(t *testing.T) {
		files, err := repo.GetFilesForPackage(ctx, pkgA.ID)
		if err != nil {
			t.Fatalf("GetFilesForPackage: %v", err)
		}
		if len(files) != 2 {
			t.Errorf("expected 2 files for A, got %d", len(files))
		}
	})

	t.Run("multiple_packages", func(t *testing.T) {
		byPkg, err := repo.GetFilesForPackages(ctx, []string{pkgA.ID, pkgB.ID})
		if err != nil {
			t.Fatalf("GetFilesForPackages: %v", err)
		}
		if len(byPkg[pkgA.ID]) != 2 {
			t.Errorf("expected 2 files for A, got %d", len(byPkg[pkgA.ID]))
		}
		if len(byPkg[pkgB.ID]) != 1 {
			t.Errorf("expected 1 file for B, got %d", len(byPkg[pkgB.ID]))
		}
	})

	t.Run("empty_input_returns_empty_map", func(t *testing.T) {
		byPkg, err := repo.GetFilesForPackages(ctx, nil)
		if err != nil {
			t.Fatalf("GetFilesForPackages nil: %v", err)
		}
		if len(byPkg) != 0 {
			t.Errorf("expected empty map, got %d entries", len(byPkg))
		}
	})
}

func TestToolPackageRepo_ResolveWireToolName(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("bamboohr_profile_provider")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}

	if err := repo.UpsertToolFile(ctx, &ToolPackageFile{
		PackageID: pkg.ID, ToolName: "enrich_authenticated_user",
		SandboxProfile: "standard", TimeoutSeconds: 30,
	}); err != nil {
		t.Fatalf("upsert bare: %v", err)
	}

	expected := pkg.Name + PackageToolSeparator + "enrich_authenticated_user"

	t.Run("reconstructs_prefixed_wire_name_from_bare_row", func(t *testing.T) {
		got, err := repo.ResolveWireToolName(ctx, pkg.ID, "enrich_authenticated_user")
		if err != nil {
			t.Fatalf("ResolveWireToolName: %v", err)
		}
		if got != expected {
			t.Errorf("wire name = %q, want %q", got, expected)
		}
	})

	t.Run("missing_tool_returns_empty", func(t *testing.T) {
		got, err := repo.ResolveWireToolName(ctx, pkg.ID, "does_not_exist")
		if err != nil {
			t.Fatalf("ResolveWireToolName: %v", err)
		}
		if got != "" {
			t.Errorf("expected empty wire name for missing tool, got %q", got)
		}
	})

	t.Run("missing_package_returns_empty", func(t *testing.T) {
		got, err := repo.ResolveWireToolName(ctx, idgen.New(), "enrich_authenticated_user")
		if err != nil {
			t.Fatalf("ResolveWireToolName: %v", err)
		}
		if got != "" {
			t.Errorf("expected empty wire name for unknown package, got %q", got)
		}
	})

	t.Run("does_not_match_other_packages_tool", func(t *testing.T) {
		pkg2 := newTestPackage("other_pkg")
		if err := repo.Create(ctx, pkg2); err != nil {
			t.Fatalf("create pkg2: %v", err)
		}
		if err := repo.UpsertToolFile(ctx, &ToolPackageFile{
			PackageID: pkg2.ID, ToolName: "ping",
			SandboxProfile: "standard", TimeoutSeconds: 30,
		}); err != nil {
			t.Fatalf("upsert pkg2 tool: %v", err)
		}
		got, err := repo.ResolveWireToolName(ctx, pkg.ID, "ping")
		if err != nil {
			t.Fatalf("ResolveWireToolName: %v", err)
		}
		if got != "" {
			t.Errorf("expected no cross-package match, got %q", got)
		}
	})
}

func TestToolPackageRepo_GetAgentsUsingPackage(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	agentRepo := NewAgentRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("pkg-users")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}

	agentA := &Agent{Name: "agent-a", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agentA, []string{pkg.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent A create: %v", err)
	}

	agentB := &Agent{Name: "agent-b", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agentB, []string{"some-other-toolset"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent B create: %v", err)
	}

	refs, err := repo.GetAgentsUsingPackage(ctx, pkg.Name)
	if err != nil {
		t.Fatalf("GetAgentsUsingPackage: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(refs))
	}
	if refs[0].ID != agentA.ID {
		t.Errorf("got agent ID %q, want %q", refs[0].ID, agentA.ID)
	}
	if refs[0].Name != "agent-a" {
		t.Errorf("got agent name %q, want agent-a", refs[0].Name)
	}

	t.Run("no_users_returns_empty", func(t *testing.T) {
		refs, err := repo.GetAgentsUsingPackage(ctx, "unused-package")
		if err != nil {
			t.Fatalf("GetAgentsUsingPackage: %v", err)
		}
		if len(refs) != 0 {
			t.Errorf("expected 0 refs, got %d", len(refs))
		}
	})
}

func TestToolPackageRepo_GetAgentsByPackageNames(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	agentRepo := NewAgentRepo(db)
	ctx := t.Context()

	pkg1 := newTestPackage("pkg-1")
	if err := repo.Create(ctx, pkg1); err != nil {
		t.Fatalf("create pkg1: %v", err)
	}
	pkg2 := newTestPackage("pkg-2")
	if err := repo.Create(ctx, pkg2); err != nil {
		t.Fatalf("create pkg2: %v", err)
	}
	pkg3 := newTestPackage("pkg-3")
	if err := repo.Create(ctx, pkg3); err != nil {
		t.Fatalf("create pkg3: %v", err)
	}

	agentA := &Agent{Name: "agent-a", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agentA, []string{pkg1.Name, pkg2.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent A create: %v", err)
	}
	agentB := &Agent{Name: "agent-b", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agentB, []string{pkg1.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent B create: %v", err)
	}
	agentC := &Agent{Name: "agent-c", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agentC, []string{"unrelated"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent C create: %v", err)
	}

	got, err := repo.GetAgentsByPackageNames(ctx, []string{pkg1.Name, pkg2.Name, pkg3.Name})
	if err != nil {
		t.Fatalf("GetAgentsByPackageNames: %v", err)
	}

	if len(got[pkg1.Name]) != 2 {
		t.Errorf("pkg1: expected 2 agents, got %d", len(got[pkg1.Name]))
	}
	if len(got[pkg2.Name]) != 1 || got[pkg2.Name][0].ID != agentA.ID {
		t.Errorf("pkg2: expected only agent-a, got %+v", got[pkg2.Name])
	}
	if len(got[pkg3.Name]) != 0 {
		t.Errorf("pkg3: expected 0 agents, got %d", len(got[pkg3.Name]))
	}
	if _, ok := got[pkg3.Name]; !ok {
		t.Errorf("pkg3: expected key present even with no matches")
	}

	t.Run("empty_input_returns_empty_map", func(t *testing.T) {
		got, err := repo.GetAgentsByPackageNames(ctx, nil)
		if err != nil {
			t.Fatalf("GetAgentsByPackageNames(nil): %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty map, got %d entries", len(got))
		}
	})

	t.Run("no_matches_returns_empty_buckets", func(t *testing.T) {
		got, err := repo.GetAgentsByPackageNames(ctx, []string{"does-not-exist"})
		if err != nil {
			t.Fatalf("GetAgentsByPackageNames: %v", err)
		}
		if len(got["does-not-exist"]) != 0 {
			t.Errorf("expected 0 agents, got %d", len(got["does-not-exist"]))
		}
	})
}

func TestToolPackageRepo_GetReadyPackagesByNames(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	ready1 := newTestPackage("ready-1")
	ready1.DiscoveryStatus = "ready"
	ready2 := newTestPackage("ready-2")
	ready2.DiscoveryStatus = "ready"
	pending := newTestPackage("pending-1")
	pending.DiscoveryStatus = "pending"

	for _, p := range []*ToolPackage{ready1, ready2, pending} {
		if err := repo.Create(ctx, p); err != nil {
			t.Fatalf("create %s: %v", p.Name, err)
		}
	}

	t.Run("only_ready_returned", func(t *testing.T) {
		got, err := repo.GetReadyPackagesByNames(ctx, []string{"ready-1", "ready-2", "pending-1", "missing"})
		if err != nil {
			t.Fatalf("GetReadyPackagesByNames: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
		seen := make(map[string]bool, 2)
		for _, p := range got {
			seen[p.Name] = true
		}
		if !seen["ready-1"] || !seen["ready-2"] {
			t.Errorf("missing expected ready packages in %v", seen)
		}
		if seen["pending-1"] {
			t.Error("pending package should not be returned")
		}
	})

	t.Run("empty_input_is_nil", func(t *testing.T) {
		got, err := repo.GetReadyPackagesByNames(ctx, nil)
		if err != nil {
			t.Fatalf("GetReadyPackagesByNames nil: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})
}

func TestToolPackageRepo_GetToolsetConfigsForAgent(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	agentRepo := NewAgentRepo(db)
	ctx := t.Context()

	pkgA := newTestPackage("tsc-a")
	pkgB := newTestPackage("tsc-b")
	if err := repo.Create(ctx, pkgA); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := repo.Create(ctx, pkgB); err != nil {
		t.Fatalf("create b: %v", err)
	}

	agent := &Agent{Name: "tsc-agent", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	cfgA := `{"apikey":"xxx"}`
	cfgB := `{"region":"us"}`
	if err := repo.SetToolsetConfigsForAgent(ctx, agent.ID, []AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: pkgA.ID, ConfigValues: &cfgA},
		{AgentID: agent.ID, PackageID: pkgB.ID, ConfigValues: &cfgB},
	}); err != nil {
		t.Fatalf("SetToolsetConfigsForAgent: %v", err)
	}

	got, err := repo.GetToolsetConfigsForAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetToolsetConfigsForAgent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(got))
	}
	byPkg := make(map[string]AgentToolsetConfig, 2)
	for _, c := range got {
		byPkg[c.PackageID] = c
	}
	if c, ok := byPkg[pkgA.ID]; !ok || c.ConfigValues == nil || *c.ConfigValues != cfgA {
		t.Errorf("pkgA config = %+v, want %q", c, cfgA)
	}
	if c, ok := byPkg[pkgB.ID]; !ok || c.ConfigValues == nil || *c.ConfigValues != cfgB {
		t.Errorf("pkgB config = %+v, want %q", c, cfgB)
	}

	t.Run("replace_clears_previous_configs", func(t *testing.T) {
		cfgNew := `{"replaced":true}`
		if err := repo.SetToolsetConfigsForAgent(ctx, agent.ID, []AgentToolsetConfig{
			{AgentID: agent.ID, PackageID: pkgA.ID, ConfigValues: &cfgNew},
		}); err != nil {
			t.Fatalf("SetToolsetConfigsForAgent replace: %v", err)
		}
		got, err := repo.GetToolsetConfigsForAgent(ctx, agent.ID)
		if err != nil {
			t.Fatalf("GetToolsetConfigsForAgent: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 config after replace, got %d", len(got))
		}
		if got[0].PackageID != pkgA.ID {
			t.Errorf("got package %q, want %q", got[0].PackageID, pkgA.ID)
		}
	})

	t.Run("unknown_agent_returns_empty", func(t *testing.T) {
		got, err := repo.GetToolsetConfigsForAgent(ctx, idgen.New())
		if err != nil {
			t.Fatalf("GetToolsetConfigsForAgent: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 configs, got %d", len(got))
		}
	})
}

func TestSetToolsetConfigsForAgent_Atomicity(t *testing.T) {
	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	agentRepo := NewAgentRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("atomic-pkg")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}

	agent := &Agent{Name: "atomic-agent", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	initialCfg := `{"original":"value"}`
	if err := repo.SetToolsetConfigsForAgent(ctx, agent.ID, []AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: pkg.ID, ConfigValues: &initialCfg},
	}); err != nil {
		t.Fatalf("SetToolsetConfigsForAgent initial: %v", err)
	}

	dupCfg := `{"dup":"a"}`
	err := repo.SetToolsetConfigsForAgent(ctx, agent.ID, []AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: pkg.ID, ConfigValues: &dupCfg},
		{AgentID: agent.ID, PackageID: pkg.ID, ConfigValues: &dupCfg},
	})
	if err == nil {
		t.Fatal("expected error from duplicate INSERT, got nil")
	}

	after, getErr := repo.GetToolsetConfigsForAgent(ctx, agent.ID)
	if getErr != nil {
		t.Fatalf("GetToolsetConfigsForAgent after failed set: %v", getErr)
	}
	if len(after) != 1 {
		t.Fatalf("expected 1 config preserved after failed atomicity (got %d) — transaction did not roll back", len(after))
	}
	if after[0].ConfigValues == nil || *after[0].ConfigValues != initialCfg {
		t.Errorf("config_values = %v, want %q — original configs were not preserved", after[0].ConfigValues, initialCfg)
	}
}

func TestSetFailedDiscovery_RoundTrip(t *testing.T) {
	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("fail-pkg")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.UpdateZipInfo(ctx, pkg.ID, "python", "s3://fake/key", "pkg.zip", 100, "tester"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}

	pending, err := repo.GetByID(ctx, pkg.ID)
	if err != nil || pending == nil {
		t.Fatalf("get after zip update: %v", err)
	}
	if pending.DiscoveryStatus != "pending" {
		t.Fatalf("expected pending, got %q", pending.DiscoveryStatus)
	}

	const errMsg = "--schema exited with code 1: module not found"
	err = repo.SetFailedDiscovery(ctx, pkg.ID, errMsg)
	if err != nil {
		t.Fatalf("SetFailedDiscovery: %v", err)
	}

	got, err := repo.GetByID(ctx, pkg.ID)
	if err != nil || got == nil {
		t.Fatalf("get after SetFailedDiscovery: %v", err)
	}
	if got.DiscoveryStatus != "failed" {
		t.Errorf("discovery_status = %q, want %q", got.DiscoveryStatus, "failed")
	}
	if got.DiscoveryErrors == nil {
		t.Fatal("discovery_errors should not be nil")
	}
	var errs []string
	if jsonErr := json.Unmarshal([]byte(*got.DiscoveryErrors), &errs); jsonErr != nil {
		t.Fatalf("unmarshal discovery_errors: %v", jsonErr)
	}
	if len(errs) != 1 || errs[0] != errMsg {
		t.Errorf("discovery_errors = %v, want [%q]", errs, errMsg)
	}
}

func TestSetFailedDiscovery_NoOpWhenReady(t *testing.T) {
	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("fail-noop-pkg")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.UpdateZipInfo(ctx, pkg.ID, "python", "s3://fake/key", "pkg.zip", 100, "tester"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}

	desc := "desc"
	params := "{}"
	toolFile := &ToolPackageFile{
		PackageID:       pkg.ID,
		ToolName:        "my_tool",
		ToolDescription: &desc,
		Parameters:      &params,
	}
	if err := repo.UpsertToolFile(ctx, toolFile); err != nil {
		t.Fatalf("UpsertToolFile: %v", err)
	}
	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatalf("flip to ready: %v", err)
	}

	ready, err := repo.GetByID(ctx, pkg.ID)
	if err != nil || ready == nil {
		t.Fatalf("get after UpsertToolFile: %v", err)
	}
	if ready.DiscoveryStatus != "ready" {
		t.Fatalf("expected ready after UpsertToolFile, got %q", ready.DiscoveryStatus)
	}

	err = repo.SetFailedDiscovery(ctx, pkg.ID, "late rebroadcast error")
	if err != nil {
		t.Fatalf("SetFailedDiscovery on ready package: %v", err)
	}

	after, err := repo.GetByID(ctx, pkg.ID)
	if err != nil || after == nil {
		t.Fatalf("get after SetFailedDiscovery: %v", err)
	}
	if after.DiscoveryStatus != "ready" {
		t.Errorf("discovery_status = %q, want %q (should be no-op)", after.DiscoveryStatus, "ready")
	}
}

func TestToolPackageRepo_ToolsetConfigRoundtrip(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := newTestPackage("ts-cfg-roundtrip")
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("get_unknown_is_nil", func(t *testing.T) {
		got, err := repo.GetToolsetConfig(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetToolsetConfig: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil for unset package, got %+v", got)
		}
	})

	t.Run("set_then_get", func(t *testing.T) {
		values := `{"api_key":"sk-abc","model_id":"gpt-4"}`
		if err := repo.SetToolsetConfig(ctx, pkg.ID, &values, "alice"); err != nil {
			t.Fatalf("SetToolsetConfig: %v", err)
		}

		got, err := repo.GetToolsetConfig(ctx, pkg.ID)
		if err != nil {
			t.Fatalf("GetToolsetConfig: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil")
		}
		if got.PackageID != pkg.ID {
			t.Errorf("package_id = %q, want %q", got.PackageID, pkg.ID)
		}
		if got.ConfigValues == nil || *got.ConfigValues != values {
			t.Errorf("config_values = %v, want %q", got.ConfigValues, values)
		}
		if got.UpdatedBy != "alice" {
			t.Errorf("updated_by = %q, want %q", got.UpdatedBy, "alice")
		}
		if got.UpdatedTime == 0 {
			t.Error("updated_time should be set")
		}
	})

	t.Run("upsert_overwrites", func(t *testing.T) {
		newValues := `{"api_key":"sk-xyz"}`
		if err := repo.SetToolsetConfig(ctx, pkg.ID, &newValues, "bob"); err != nil {
			t.Fatalf("SetToolsetConfig upsert: %v", err)
		}

		got, err := repo.GetToolsetConfig(ctx, pkg.ID)
		if err != nil || got == nil {
			t.Fatalf("GetToolsetConfig: %v", err)
		}
		if got.ConfigValues == nil || *got.ConfigValues != newValues {
			t.Errorf("config_values = %v, want %q", got.ConfigValues, newValues)
		}
		if got.UpdatedBy != "bob" {
			t.Errorf("updated_by = %q, want %q (upsert should update author)", got.UpdatedBy, "bob")
		}
	})

	t.Run("nil_clears_values", func(t *testing.T) {
		if err := repo.SetToolsetConfig(ctx, pkg.ID, nil, "carol"); err != nil {
			t.Fatalf("SetToolsetConfig nil: %v", err)
		}
		got, err := repo.GetToolsetConfig(ctx, pkg.ID)
		if err != nil || got == nil {
			t.Fatalf("GetToolsetConfig: %v", err)
		}
		if got.ConfigValues != nil {
			t.Errorf("config_values = %v, want nil after clear", *got.ConfigValues)
		}
		if got.UpdatedBy != "carol" {
			t.Errorf("updated_by = %q, want %q", got.UpdatedBy, "carol")
		}
	})

	t.Run("cascade_delete_with_package", func(t *testing.T) {
		other := newTestPackage("ts-cfg-cascade")
		if err := repo.Create(ctx, other); err != nil {
			t.Fatalf("create cascade pkg: %v", err)
		}
		v := `{"k":"v"}`
		if err := repo.SetToolsetConfig(ctx, other.ID, &v, "alice"); err != nil {
			t.Fatalf("SetToolsetConfig: %v", err)
		}
		if err := repo.Delete(ctx, other.ID); err != nil {
			t.Fatalf("Delete package: %v", err)
		}
		got, err := repo.GetToolsetConfig(ctx, other.ID)
		if err != nil {
			t.Fatalf("GetToolsetConfig after cascade: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil after cascade delete, got %+v", got)
		}
	})
}

func TestToolPackageRepo_GetToolsetConfigsForPackages(t *testing.T) {

	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()

	pkgA := newTestPackage("ts-batch-a")
	pkgB := newTestPackage("ts-batch-b")
	pkgC := newTestPackage("ts-batch-c")
	for _, p := range []*ToolPackage{pkgA, pkgB, pkgC} {
		if err := repo.Create(ctx, p); err != nil {
			t.Fatalf("create %s: %v", p.Name, err)
		}
	}
	valA := `{"a":1}`
	valB := `{"b":2}`
	if err := repo.SetToolsetConfig(ctx, pkgA.ID, &valA, "u"); err != nil {
		t.Fatalf("set A: %v", err)
	}
	if err := repo.SetToolsetConfig(ctx, pkgB.ID, &valB, "u"); err != nil {
		t.Fatalf("set B: %v", err)
	}

	got, err := repo.GetToolsetConfigsForPackages(ctx, []string{pkgA.ID, pkgB.ID, pkgC.ID})
	if err != nil {
		t.Fatalf("GetToolsetConfigsForPackages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries (C has no config), got %d", len(got))
	}
	if got[pkgA.ID].ConfigValues == nil || *got[pkgA.ID].ConfigValues != valA {
		t.Errorf("pkgA values = %v, want %q", got[pkgA.ID].ConfigValues, valA)
	}
	if got[pkgB.ID].ConfigValues == nil || *got[pkgB.ID].ConfigValues != valB {
		t.Errorf("pkgB values = %v, want %q", got[pkgB.ID].ConfigValues, valB)
	}
	if _, ok := got[pkgC.ID]; ok {
		t.Errorf("pkgC should be absent from result map")
	}

	t.Run("empty_input_returns_empty", func(t *testing.T) {
		got, err := repo.GetToolsetConfigsForPackages(ctx, nil)
		if err != nil {
			t.Fatalf("GetToolsetConfigsForPackages nil: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 entries, got %d", len(got))
		}
	})
}

func TestToolPackageRepo_FailStalePending(t *testing.T) {
	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	ctx := t.Context()
	pkg := newTestPackage("pkg-stale-pending")
	pkg.DiscoveryStatus = "pending"
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour).UnixMilli()
	reason := "discovery expired"

	if failed, err := repo.FailStalePending(ctx, pkg.ID, time.Now().Add(-time.Hour).UnixMilli(), &reason); err != nil || failed {
		t.Fatalf("FailStalePending before the package went pending = %v, %v; want it kept", failed, err)
	}
	if failed, err := repo.FailStalePending(ctx, pkg.ID, future, &reason); err != nil || !failed {
		t.Fatalf("FailStalePending = %v, %v; want the pending package failed", failed, err)
	}
	if got, _ := repo.GetByID(ctx, pkg.ID); got.DiscoveryStatus != "failed" {
		t.Fatalf("status = %q, want failed", got.DiscoveryStatus)
	}

	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatal(err)
	}
	if failed, err := repo.FailStalePending(ctx, pkg.ID, future, &reason); err != nil || failed {
		t.Fatalf("FailStalePending on a discovered package = %v, %v; want it kept", failed, err)
	}
	if got, _ := repo.GetByID(ctx, pkg.ID); got.DiscoveryStatus != "ready" {
		t.Errorf("status = %q, want ready kept", got.DiscoveryStatus)
	}
}
