// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !embeddocs

package docsembed

import "testing"

func TestUnavailableWithoutTag(t *testing.T) {
	if Available() {
		t.Error("Available() = true, want false without the embeddocs tag")
	}
	if fsys := FS(); fsys != nil {
		t.Error("FS() returned a non-nil filesystem without the embeddocs tag")
	}
}
