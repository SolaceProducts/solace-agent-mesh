// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import "testing"

func TestAddWithheldComponentRefusesADuplicateName(t *testing.T) {
	const name = "duplicate-check"
	none := func(WithheldDeps) (*WithheldComponent, error) { return nil, nil }
	AddWithheldComponent(name, none)
	t.Cleanup(func() { delete(withheldComponents, name) })
	defer func() {
		if recover() == nil {
			t.Errorf("AddWithheldComponent(%q) twice did not panic", name)
		}
	}()
	AddWithheldComponent(name, none)
}
