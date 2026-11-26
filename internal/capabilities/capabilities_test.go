// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package capabilities

import (
	"slices"
	"strings"
	"testing"
)

func TestResolveMatrix(t *testing.T) {
	tests := []struct {
		name string
		goos string
		key  string
		want Maturity
	}{
		{"baseline applies to unlisted platform", "linux", "event_mesh_gateway", MaturityStable},
		{"platform override wins over baseline", "windows", "event_mesh_gateway", MaturityBeta},
		{"darwin uses baseline", "darwin", "event_mesh_gateway", MaturityStable},
		{"connector GA baseline on linux", "linux", "event_mesh_connector", MaturityStable},
		{"connector beta override on windows", "windows", "event_mesh_connector", MaturityBeta},
		{"connector darwin uses GA baseline", "darwin", "event_mesh_connector", MaturityStable},
		{"unknown key resolves unavailable", "linux", "does_not_exist", MaturityUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := NewRegistryForPlatform(tt.goos, nil)
			if got := r.Resolve(tt.key); got != tt.want {
				t.Errorf("Resolve(%q) on %s = %q, want %q", tt.key, tt.goos, got, tt.want)
			}
		})
	}
}

func TestEnvOverrideDisables(t *testing.T) {
	t.Setenv("SAM_CAPABILITY_EVENT_MESH_GATEWAY", "false")
	r := NewRegistryForPlatform("windows", nil)
	if got := r.Resolve("event_mesh_gateway"); got != MaturityUnavailable {
		t.Errorf("env override: got %q, want unavailable", got)
	}
	resolved := findResolved(r.All(), "event_mesh_gateway")
	if resolved == nil || !resolved.HasEnvOverride {
		t.Errorf("expected HasEnvOverride=true, got %+v", resolved)
	}
}

func TestAllReportsPlatformAndWireShape(t *testing.T) {
	r := NewRegistryForPlatform("windows", nil)
	resolved := findResolved(r.All(), "event_mesh_gateway")
	if resolved == nil {
		t.Fatal("event_mesh_gateway missing from All()")
	}
	if resolved.Platform != "windows" {
		t.Errorf("Platform = %q, want windows", resolved.Platform)
	}
	if resolved.Status != MaturityBeta {
		t.Errorf("Status = %q, want beta", resolved.Status)
	}
}

func TestUnlistedPlatformBaselineFailsClosedWithoutBaseline(t *testing.T) {
	entry := Entry{Key: "k", Platforms: map[string]Maturity{"windows": MaturityExperimental}}
	if m, rsn := resolveFor(entry, "linux", false); m != MaturityUnavailable || rsn != reasonUnconfigured {
		t.Errorf("no-baseline unlisted platform = (%q,%d), want (unavailable, unconfigured)", m, rsn)
	}
	if m, rsn := resolveFor(entry, "windows", false); m != MaturityExperimental || rsn != reasonAvailable {
		t.Errorf("listed platform = (%q,%d), want (experimental, available)", m, rsn)
	}
}

func TestPerOSUnavailableOverrideIsDeliberate(t *testing.T) {
	entry := Entry{Key: "k", Maturity: MaturityStable, Platforms: map[string]Maturity{"darwin": MaturityUnavailable}}
	if m, rsn := resolveFor(entry, "darwin", false); m != MaturityUnavailable || rsn != reasonPlatformExcluded {
		t.Errorf("darwin override = (%q,%d), want (unavailable, platformExcluded)", m, rsn)
	}
	if m, _ := resolveFor(entry, "linux", false); m != MaturityStable {
		t.Errorf("linux baseline = %q, want stable", m)
	}
}

func TestEmbeddedCatalogIsClean(t *testing.T) {
	if w := NewRegistryForPlatform("linux", nil).Lint(); len(w) != 0 {
		t.Errorf("embedded catalog should lint clean, got: %v", w)
	}
}

func TestLintFlagsTypoGOOS(t *testing.T) {
	r := &Registry{goos: "linux", entries: []Entry{{Key: "k", Maturity: MaturityStable, Platforms: map[string]Maturity{"windwos": MaturityExperimental}}}}
	if !containsSubstr(r.Lint(), `unknown platform "windwos"`) {
		t.Errorf("expected typo'd GOOS warning, got: %v", r.Lint())
	}
}

func TestLintFlagsInvalidMaturity(t *testing.T) {
	r := &Registry{goos: "linux", entries: []Entry{{Key: "k", Maturity: "expermental"}}}
	if !containsSubstr(r.Lint(), `unknown maturity "expermental"`) {
		t.Errorf("expected invalid maturity warning, got: %v", r.Lint())
	}
}

func TestLintFlagsUnconfiguredHosts(t *testing.T) {
	r := &Registry{goos: "windows", entries: []Entry{{Key: "k", Platforms: map[string]Maturity{"windows": MaturityExperimental}}}}
	warns := r.Lint()
	if !containsSubstr(warns, "will be unavailable on") || !containsSubstr(warns, "linux") {
		t.Errorf("expected unconfigured-hosts warning listing linux, got: %v", warns)
	}
}

func TestLintFlagsUnknownEnvOverride(t *testing.T) {
	t.Setenv("SAM_CAPABILITY_TYPO_KEY", "false")
	r := &Registry{goos: "linux", entries: []Entry{{Key: "k", Maturity: MaturityStable}}}
	if !containsSubstr(r.Lint(), "SAM_CAPABILITY_TYPO_KEY") {
		t.Errorf("expected unknown env override warning, got: %v", r.Lint())
	}
}

func findResolved(all []Resolved, key string) *Resolved {
	for i := range all {
		if all[i].Key == key {
			return &all[i]
		}
	}
	return nil
}

func containsSubstr(warns []string, sub string) bool {
	return slices.ContainsFunc(warns, func(w string) bool { return strings.Contains(w, sub) })
}
