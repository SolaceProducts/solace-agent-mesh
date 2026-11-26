// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestToolService_UpsertFromInit_CreatesThenSkipsUnchanged(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolRepo(db)
	svc := NewToolService(repo, slog.Default())
	ctx := context.Background()

	params := remotetool.InitParams{
		ToolName:    "pkg__do_thing",
		Description: "does the thing",
		Parameters:  map[string]any{"type": "object"},
	}

	svc.UpsertFromInit(ctx, params)
	first, err := repo.GetByName(ctx, params.ToolName)
	if err != nil || first == nil {
		t.Fatalf("tool should be created on first init: tool=%v err=%v", first, err)
	}

	time.Sleep(5 * time.Millisecond)

	svc.UpsertFromInit(ctx, params)
	second, err := repo.GetByName(ctx, params.ToolName)
	if err != nil || second == nil {
		t.Fatalf("second GetByName: tool=%v err=%v", second, err)
	}
	if second.UpdatedTime != first.UpdatedTime {
		t.Fatalf("identical re-broadcast rewrote the row: updated_time first=%d second=%d",
			first.UpdatedTime, second.UpdatedTime)
	}
}

func TestToolService_UpsertFromInit_AppliesChange(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolRepo(db)
	svc := NewToolService(repo, slog.Default())
	ctx := context.Background()

	svc.UpsertFromInit(ctx, remotetool.InitParams{ToolName: "pkg__evolve", Description: "v1"})

	svc.MarkUnavailable(ctx, "pkg__evolve")

	svc.UpsertFromInit(ctx, remotetool.InitParams{ToolName: "pkg__evolve", Description: "v2"})
	got, err := repo.GetByName(ctx, "pkg__evolve")
	if err != nil || got == nil {
		t.Fatalf("GetByName: tool=%v err=%v", got, err)
	}
	if got.Description == nil || *got.Description != "v2" {
		t.Errorf("Description not updated: %v", got.Description)
	}
	if got.Status != "available" {
		t.Errorf("Status = %q, want available after re-init", got.Status)
	}
}

func TestToolFileUnchanged(t *testing.T) {
	t.Parallel()
	base := func() *store.ToolPackageFile {
		desc, params, schema, auth := "d", `{"p":1}`, `[{"key":"k"}]`, `{"type":"oauth"}`
		return &store.ToolPackageFile{
			ToolName: "pkg__do", ToolDescription: &desc, TimeoutSeconds: 300,
			SandboxProfile: "standard", Parameters: &params, ConfigSchema: &schema, Auth: &auth,
		}
	}
	cases := []struct {
		name   string
		mutate func(*store.ToolPackageFile)
		want   bool
	}{
		{"identical", func(*store.ToolPackageFile) {}, true},
		{"description differs", func(f *store.ToolPackageFile) { d := "d2"; f.ToolDescription = &d }, false},
		{"timeout differs", func(f *store.ToolPackageFile) { f.TimeoutSeconds = 60 }, false},
		{"sandbox differs", func(f *store.ToolPackageFile) { f.SandboxProfile = "locked" }, false},
		{"parameters differ", func(f *store.ToolPackageFile) { p := `{"p":2}`; f.Parameters = &p }, false},
		{"config schema differs", func(f *store.ToolPackageFile) { s := `[]`; f.ConfigSchema = &s }, false},
		{"auth differs", func(f *store.ToolPackageFile) { f.Auth = nil }, false},
		{"no matching tool name", func(f *store.ToolPackageFile) { f.ToolName = "pkg__other" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			incoming := base()
			tc.mutate(incoming)
			if got := toolFileUnchanged([]store.ToolPackageFile{*base()}, incoming); got != tc.want {
				t.Errorf("toolFileUnchanged = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestToolsetService_HandleToolInit_ReadinessSurvivesSkippedWrite(t *testing.T) {
	t.Parallel()
	f := newOutboxFixture(t)
	ctx := t.Context()

	pkg := f.createPkg(ctx, t, "reinit")
	if err := f.repo.UpdateZipInfo(ctx, pkg.ID, "python", "s3://fake", "pkg.zip", 1, "u"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}
	params := `{}`
	f.svc.HandleToolInit(ctx, "reinit__do", "desc", ToolInitMeta{Params: &params})

	if err := f.repo.UpdateDiscoveryStatus(ctx, pkg.ID, "pending", nil); err != nil {
		t.Fatalf("reset discovery status: %v", err)
	}

	f.svc.HandleToolInit(ctx, "reinit__do", "desc", ToolInitMeta{Params: &params})

	got, err := f.repo.GetByID(ctx, pkg.ID)
	if err != nil || got == nil {
		t.Fatalf("GetByID: pkg=%v err=%v", got, err)
	}
	if got.DiscoveryStatus != "ready" {
		t.Errorf("DiscoveryStatus = %q, want ready after identical re-init (readiness must not be gated by the write skip)", got.DiscoveryStatus)
	}
}

func TestToolService_UpsertFromInit_RefreshesAnUnchangedToolHourly(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolRepo(db)
	svc := NewToolService(repo, slog.Default())
	ctx := t.Context()
	params := remotetool.InitParams{ToolName: "baked_tool", Description: "baked into the image"}

	svc.UpsertFromInit(ctx, params)
	first, err := repo.GetByName(ctx, params.ToolName)
	if err != nil || first == nil {
		t.Fatalf("first init: tool=%v err=%v", first, err)
	}
	announced := time.UnixMilli(first.UpdatedTime)

	svc.now = func() time.Time { return announced.Add(30 * time.Minute) }
	svc.UpsertFromInit(ctx, params)
	if again, _ := repo.GetByName(ctx, params.ToolName); again.UpdatedTime != first.UpdatedTime {
		t.Fatalf("a re-announcement within the hour moved updated_time %d -> %d", first.UpdatedTime, again.UpdatedTime)
	}

	later := announced.Add(61 * time.Minute)
	svc.now = func() time.Time { return later }
	svc.UpsertFromInit(ctx, params)
	if refreshed, _ := repo.GetByName(ctx, params.ToolName); refreshed.UpdatedTime != later.UnixMilli() {
		t.Errorf("a re-announcement past the hour left updated_time at %d, want %d", refreshed.UpdatedTime, later.UnixMilli())
	}
}
