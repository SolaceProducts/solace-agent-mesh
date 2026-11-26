// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import "testing"

func TestResolve(t *testing.T) {
	assigned, tierDefault := new("assigned"), new("default")
	for _, c := range []struct {
		name                  string
		assigned, tierDefault *string
		want                  *string
		source                Source
	}{
		{"an assignment wins over the default", assigned, tierDefault, assigned, SourceAssigned},
		{"an assignment with no default", assigned, nil, assigned, SourceAssigned},
		{"the default when unassigned", nil, tierDefault, tierDefault, SourceDefault},
		{"neither", nil, nil, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, source := Resolve(c.assigned, c.tierDefault)
			if got != c.want || source != c.source {
				t.Errorf("Resolve = %v, %q; want %v, %q", got, source, c.want, c.source)
			}
		})
	}
}
