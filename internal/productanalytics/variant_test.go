// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"testing"
)

func TestEveryEventCarriesTheBuildVariant(t *testing.T) {
	local := relayService()
	local.Enqueue(context.Background(), SurfaceCLI, []RawEvent{cliRun()})
	relayed := relayService()
	relayed.EnqueueRelayed(context.Background(), SurfaceCLI, localPlatform(), []RawEvent{cliRun()})

	for name, s := range map[string]*Service{"local": local, "relayed": relayed} {
		buf := buffered(s)
		if len(buf) != 1 {
			t.Fatalf("%s: buffered = %d, want 1", name, len(buf))
		}
		if got := buf[0].Properties[PropBuildVariant]; got != wantBuildVariant {
			t.Errorf("%s: buildVariant = %v, want %q", name, got, wantBuildVariant)
		}
	}
}

func TestAClientCannotChooseTheBuildVariant(t *testing.T) {
	s := relayService()
	raw := cliRun()
	raw.Properties[PropBuildVariant] = "forged"
	s.EnqueueRelayed(context.Background(), SurfaceCLI, localPlatform(), []RawEvent{raw})

	buf := buffered(s)
	if len(buf) != 1 {
		t.Fatalf("buffered = %d, want 1", len(buf))
	}
	if got := buf[0].Properties[PropBuildVariant]; got != wantBuildVariant {
		t.Errorf("buildVariant = %v, want %q", got, wantBuildVariant)
	}
}
