// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build solace_id

package productanalytics

import (
	"context"
	"testing"
)

const wantBuildVariant = "solace_id"

func TestASolaceIDBuildTagsItsEventsSolaceID(t *testing.T) {
	s := relayService()
	s.Enqueue(context.Background(), SurfaceCLI, []RawEvent{cliRun()})

	if got := buffered(s)[0].Properties[PropBuildVariant]; got != wantBuildVariant {
		t.Errorf("buildVariant = %v, want %q", got, wantBuildVariant)
	}
}
