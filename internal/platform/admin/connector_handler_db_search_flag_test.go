// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "testing"

func TestConnectorSubtypeIsExperimental(t *testing.T) {
	cases := []struct {
		typ, subtype string
		want         bool
	}{
		{"document_db", "mongodb", false},
		{"document_db", "dynamodb", true},
		{"graph_db", "neo4j", true},
		{"search", "elasticsearch", true},
		{"web_search", "tavily", false},
		{"web_search", "exa", false},
		{"event_mesh", "event_mesh", false},
		{"sql", "postgres", false},
	}
	for _, c := range cases {
		if got := connectorSubtypeIsExperimental(c.typ, c.subtype); got != c.want {
			t.Errorf("connectorSubtypeIsExperimental(%q, %q) = %v, want %v", c.typ, c.subtype, got, c.want)
		}
	}
}
