// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"errors"
	"testing"
)

func TestDesiredForKind_CoversEveryPersistedKind(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	for _, kind := range componentKindsInOrder() {
		_, err := desiredForKind(kind, "x", "", cat)
		if err == nil {
			continue
		}
		if errors.Is(err, errKindNotPersisted) {
			t.Errorf("kind %q is in componentKindsInOrder but has no converter", kind)
		}
	}
}

func TestDesiredForKind_RejectsUnpersistedKind(t *testing.T) {
	t.Parallel()
	if _, err := desiredForKind(kindSkill, "s", "name: s", newToolsetCatalog()); err == nil {
		t.Fatal("skills are not persisted by this endpoint; expected an error")
	}
}
