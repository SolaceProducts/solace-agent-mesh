// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func writeToolsetSourceDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "tool.py"), []byte("# stub"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return d
}

func TestPlanToolsets_CreateFlagsConfigChangedWhenSpecPresent(t *testing.T) {
	dir := writeToolsetSourceDir(t)
	desired := []resolve.ResolvedToolset{{
		Name:       "demo",
		SourceFile: filepath.Join(dir, "manifest.yaml"),
		SourceDir:  dir,
		Config:     map[string]any{"api_base": "https://example.com"},
	}}
	plan, err := planToolsets(context.Background(), nil, desired, nil, t.TempDir(), BuildOptions{NoBuild: true}, nil)
	if err != nil {
		t.Fatalf("planToolsets: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected 1 create, got %d", len(plan.Creates))
	}
	if !plan.Creates[0].ConfigChanged {
		t.Errorf("Create op missing ConfigChanged=true; the apply step will skip the PATCH /config call")
	}
}

func TestPlanToolsets_UpdateFlagsConfigChanged(t *testing.T) {
	dir := writeToolsetSourceDir(t)
	desired := []resolve.ResolvedToolset{{
		Name:       "demo",
		SourceFile: filepath.Join(dir, "manifest.yaml"),
		SourceDir:  dir,
		Config:     map[string]any{"api_base": "https://new.example.com"},
	}}

	storedZipName := "demo.zip"
	actual := []wire.ToolsetResponse{{
		ID:     "id-1",
		Name:   "demo",
		Bundle: &apiv2.ToolsetBundle{Filename: storedZipName},
	}}

	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{Values: map[string]any{"api_base": "https://old.example.com"}}, nil
	}
	plan, err := planToolsets(context.Background(), nil, desired, actual, t.TempDir(), BuildOptions{NoBuild: true}, fetch)
	if err != nil {
		t.Fatalf("planToolsets: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update, got %d (creates=%d unchanged=%d)", len(plan.Updates), len(plan.Creates), len(plan.Unchanged))
	}
	if !plan.Updates[0].ConfigChanged {
		t.Errorf("Update op missing ConfigChanged=true; re-plans would render no config diff")
	}
}

func TestPlanToolsets_NoConfigStaysUnchanged(t *testing.T) {
	dir := writeToolsetSourceDir(t)
	desired := []resolve.ResolvedToolset{{
		Name:       "demo",
		SourceFile: filepath.Join(dir, "manifest.yaml"),
		SourceDir:  dir,
	}}

	td, err := loadToolsetDesired(context.Background(), desired[0], t.TempDir(), BuildOptions{NoBuild: true})
	if err != nil {
		t.Fatalf("loadToolsetDesired: %v", err)
	}
	storedZipName := "demo.zip"
	actual := []wire.ToolsetResponse{{
		ID:     "id-1",
		Name:   "demo",
		Bundle: &apiv2.ToolsetBundle{Filename: storedZipName, ContentHash: new(td.bundleHash)},
	}}

	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{Values: map[string]any{}}, nil
	}
	plan, err := planToolsets(context.Background(), nil, desired, actual, t.TempDir(), BuildOptions{NoBuild: true}, fetch)
	if err != nil {
		t.Fatalf("planToolsets: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("expected 1 unchanged, got updates=%d creates=%d unchanged=%d", len(plan.Updates), len(plan.Creates), len(plan.Unchanged))
	}
}

func TestPlanToolsets_RotatedAuthClientSecretPromotedToUpdate(t *testing.T) {
	dir := writeToolsetSourceDir(t)
	authConfig := map[string]any{
		"credential": map[string]any{"client_id": "cid", "client_secret": "rotated"},
	}
	desired := []resolve.ResolvedToolset{{
		Name:       "demo",
		SourceFile: filepath.Join(dir, "manifest.yaml"),
		SourceDir:  dir,
		Config:     map[string]any{"auth": authConfig},
	}}
	td, err := loadToolsetDesired(context.Background(), desired[0], t.TempDir(), BuildOptions{NoBuild: true})
	if err != nil {
		t.Fatalf("loadToolsetDesired: %v", err)
	}
	storedZipName := "demo.zip"
	actual := []wire.ToolsetResponse{{
		ID: "id-1", Name: "demo",
		Bundle: &apiv2.ToolsetBundle{Filename: storedZipName, ContentHash: new(td.bundleHash)},
	}}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{Values: map[string]any{
			"auth": map[string]any{
				"credential": map[string]any{"client_id": "cid", "client_secret": redactedSentinel},
			},
		}}, nil
	}

	for _, tc := range []struct {
		name        string
		matches     bool
		wantUpdates int
	}{
		{"rotated", false, 1},
		{"unchanged", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			differ := &stubSecretDiffer{matchByID: map[string]bool{"id-1": tc.matches}}
			plan, planErr := planToolsets(context.Background(), differ, desired, actual, t.TempDir(), BuildOptions{NoBuild: true}, fetch)
			if planErr != nil {
				t.Fatalf("planToolsets: %v", planErr)
			}
			if len(plan.Updates) != tc.wantUpdates || len(plan.Unchanged) != 1-tc.wantUpdates {
				t.Fatalf("updates=%d unchanged=%d; want updates=%d", len(plan.Updates), len(plan.Unchanged), tc.wantUpdates)
			}
			if len(differ.calls) != 1 || differ.calls[0].kind != "toolsets" {
				t.Fatalf("expected one toolsets DiffSecrets call; got %+v", differ.calls)
			}
			candidate, _ := differ.calls[0].values.(map[string]any)
			if !reflect.DeepEqual(candidate["auth"], authConfig) {
				t.Errorf("candidate auth = %+v; want the desired auth block", candidate["auth"])
			}
			if tc.wantUpdates == 1 && (!plan.Updates[0].ConfigChanged || plan.Updates[0].UploadNeeded) {
				t.Errorf("promoted op = %+v; want ConfigChanged=true, UploadNeeded=false", plan.Updates[0])
			}
		})
	}
}

func TestPlanToolsets_UndeclaredConfigKeyFailsWhenBundleUnchanged(t *testing.T) {
	dir := writeToolsetSourceDir(t)
	desired := []resolve.ResolvedToolset{{
		Name:       "ledger",
		SourceFile: filepath.Join(dir, "manifest.yaml"),
		SourceDir:  dir,
		Config:     map[string]any{"api_base": "https://x", "ledger_api_key": "sk"},
	}}
	td, err := loadToolsetDesired(context.Background(), desired[0], t.TempDir(), BuildOptions{NoBuild: true})
	if err != nil {
		t.Fatalf("loadToolsetDesired: %v", err)
	}
	storedZipName := "ledger.zip"
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "ledger", Bundle: &apiv2.ToolsetBundle{Filename: storedZipName}, DiscoveryStatus: new("ready"),
		Tools: []apiv2.ToolsetTool{{ToolName: "post_entry", ConfigSchema: []any{map[string]any{"key": "api_base"}}}},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{Values: map[string]any{}}, nil
	}

	unchanged := existing
	unchanged.Bundle = &apiv2.ToolsetBundle{Filename: storedZipName, ContentHash: new(td.bundleHash)}
	_, err = planToolsets(context.Background(), nil, desired, []wire.ToolsetResponse{unchanged}, t.TempDir(), BuildOptions{NoBuild: true}, fetch)
	if err == nil || !strings.Contains(err.Error(), "ledger_api_key") {
		t.Fatalf("unchanged bundle: want undeclared-key error naming ledger_api_key, got %v", err)
	}

	discovering := unchanged
	discovering.DiscoveryStatus = new("pending")
	if _, err = planToolsets(context.Background(), nil, desired, []wire.ToolsetResponse{discovering}, t.TempDir(), BuildOptions{NoBuild: true}, fetch); err != nil {
		t.Fatalf("discovery in flight: plan must defer the key check to apply, got %v", err)
	}

	changing := existing
	changing.Bundle = &apiv2.ToolsetBundle{Filename: storedZipName, ContentHash: new("stale")}
	plan, err := planToolsets(context.Background(), nil, desired, []wire.ToolsetResponse{changing}, t.TempDir(), BuildOptions{NoBuild: true}, fetch)
	if err != nil {
		t.Fatalf("changing bundle: plan must defer the key check to apply, got %v", err)
	}
	if len(plan.Updates) != 1 || !plan.Updates[0].UploadNeeded {
		t.Fatalf("changing bundle: want 1 re-upload update, got %+v", plan.Updates)
	}
}

func TestPlanToolsets_FailedDiscoveryForcesReuploadAndPropagatesStatus(t *testing.T) {
	dir := writeToolsetSourceDir(t)
	desired := []resolve.ResolvedToolset{{
		Name:       "demo",
		SourceFile: filepath.Join(dir, "manifest.yaml"),
		SourceDir:  dir,
		Config:     map[string]any{"api_base": "https://example.com"},
	}}

	td, err := loadToolsetDesired(t.Context(), desired[0], t.TempDir(), BuildOptions{NoBuild: true})
	if err != nil {
		t.Fatalf("loadToolsetDesired: %v", err)
	}
	storedZipName := "demo.zip"
	actual := []wire.ToolsetResponse{{
		ID:              "id-1",
		Name:            "demo",
		Bundle:          &apiv2.ToolsetBundle{Filename: storedZipName, ContentHash: new(td.bundleHash)},
		DiscoveryStatus: new("failed"),
		DiscoveryErrors: []string{"binary cannot run on this host: STR is running on linux/arm64"},
	}}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{Values: map[string]any{"api_base": "https://example.com"}}, nil
	}
	plan, err := planToolsets(t.Context(), nil, desired, actual, t.TempDir(), BuildOptions{NoBuild: true}, fetch)
	if err != nil {
		t.Fatalf("planToolsets: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update for failed toolset, got creates=%d updates=%d unchanged=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Unchanged))
	}
	op := plan.Updates[0]
	if !op.UploadNeeded {
		t.Errorf("UploadNeeded should be true so apply retriggers STR discovery; got false")
	}
	if op.DiscoveryStatus != "failed" {
		t.Errorf("DiscoveryStatus not propagated: got %q want %q", op.DiscoveryStatus, "failed")
	}
	if len(op.DiscoveryErrors) != 1 || op.DiscoveryErrors[0] != "binary cannot run on this host: STR is running on linux/arm64" {
		t.Errorf("DiscoveryErrors not propagated; got %v", op.DiscoveryErrors)
	}
}

func TestPlanToolsets_ContentHashDiffDrivesReuploadEvenWhenSizeMatches(t *testing.T) {
	dir := writeToolsetSourceDir(t)
	desired := []resolve.ResolvedToolset{{
		Name:       "demo",
		SourceFile: filepath.Join(dir, "manifest.yaml"),
		SourceDir:  dir,
	}}

	td, err := loadToolsetDesired(t.Context(), desired[0], t.TempDir(), BuildOptions{NoBuild: true})
	if err != nil {
		t.Fatalf("loadToolsetDesired: %v", err)
	}
	storedZipName := "demo.zip"

	matching := []wire.ToolsetResponse{{
		ID:              "id-1",
		Name:            "demo",
		Bundle:          &apiv2.ToolsetBundle{Filename: storedZipName, ContentHash: new(td.bundleHash)},
		DiscoveryStatus: new("ready"),
	}}
	plan, err := planToolsets(t.Context(), nil, desired, matching, t.TempDir(), BuildOptions{NoBuild: true}, nil)
	if err != nil {
		t.Fatalf("planToolsets(matching hash): %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("matching hash: expected 1 unchanged, got creates=%d updates=%d unchanged=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Unchanged))
	}

	localSize := int64(len(td.bundleData))
	differing := []wire.ToolsetResponse{{
		ID:              "id-1",
		Name:            "demo",
		Bundle:          &apiv2.ToolsetBundle{Filename: storedZipName, SizeBytes: &localSize, ContentHash: new("0000000000000000000000000000000000000000000000000000000000000000")},
		DiscoveryStatus: new("ready"),
	}}
	plan, err = planToolsets(t.Context(), nil, desired, differing, t.TempDir(), BuildOptions{NoBuild: true}, nil)
	if err != nil {
		t.Fatalf("planToolsets(differing hash): %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("differing hash: expected 1 update, got creates=%d updates=%d unchanged=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Unchanged))
	}
	if !plan.Updates[0].UploadNeeded {
		t.Errorf("differing hash must set UploadNeeded=true; apply will skip /toolsets/{id}/upload otherwise")
	}
}

func TestPlanToolsets_NullContentHashForcesReupload(t *testing.T) {
	dir := writeToolsetSourceDir(t)
	desired := []resolve.ResolvedToolset{{
		Name:       "demo",
		SourceFile: filepath.Join(dir, "manifest.yaml"),
		SourceDir:  dir,
	}}

	storedZipName := "demo.zip"
	actual := []wire.ToolsetResponse{{
		ID:              "id-1",
		Name:            "demo",
		Bundle:          &apiv2.ToolsetBundle{Filename: storedZipName},
		DiscoveryStatus: new("ready"),
	}}
	plan, err := planToolsets(t.Context(), nil, desired, actual, t.TempDir(), BuildOptions{NoBuild: true}, nil)
	if err != nil {
		t.Fatalf("planToolsets(empty hash): %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("empty hash: expected 1 update, got creates=%d updates=%d unchanged=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Unchanged))
	}
	if !plan.Updates[0].UploadNeeded {
		t.Errorf("empty/NULL stored hash must set UploadNeeded=true so an upgraded platform re-uploads existing toolsets")
	}
}

func TestMustMap(t *testing.T) {
	cases := []any{nil, 42, "scalar", []string{"a"}}
	for _, in := range cases {
		got := mustMap(in)
		if got == nil || len(got) != 0 {
			t.Errorf("mustMap(%v) = %v; want empty map[string]any{}", in, got)
		}
	}
	typed := map[string]any{"k": "v"}
	got := mustMap(typed)
	if got["k"] != "v" {
		t.Errorf("mustMap(typed) lost values: %v", got)
	}
}
