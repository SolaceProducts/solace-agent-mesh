// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package capabilities

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

var maturityForPhase = map[features.ReleasePhase]Maturity{
	features.PhaseExperimental:        MaturityExperimental,
	features.PhaseEarlyAccess:         MaturityEarlyAccess,
	features.PhaseBeta:                MaturityBeta,
	features.PhaseGeneralAvailability: MaturityStable,
}

func TestCatalogBaselineMatchesFeatureReleasePhase(t *testing.T) {
	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatalf("load feature flags: %v", err)
	}

	compared := 0
	for _, e := range NewRegistryForPlatform("linux", nil).entries {
		flag, ok := flags.Get(e.Key)
		if !ok {
			continue
		}
		compared++
		want, ok := maturityForPhase[flag.ReleasePhase]
		if !ok {
			t.Errorf("%q: release_phase %q has no capability maturity equivalent; add one to maturityForPhase", e.Key, flag.ReleasePhase)
			continue
		}
		if e.Maturity != want {
			t.Errorf("%q: capability baseline maturity %q disagrees with feature release_phase %q (want %q)", e.Key, e.Maturity, flag.ReleasePhase, want)
		}
	}
	if compared == 0 {
		t.Fatal("no key appears in both the capability catalog and features.yaml, so nothing was compared; a rename on one side has broken the pairing")
	}
}

func TestGoogleChatGatewayIsBadgedEarlyAccessOnEveryPlatform(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if got := NewRegistryForPlatform(goos, nil).Resolve("google_chat_gateway"); got != MaturityEarlyAccess {
			t.Errorf("google_chat_gateway on %s resolves to %q, want %q", goos, got, MaturityEarlyAccess)
		}
	}
}
