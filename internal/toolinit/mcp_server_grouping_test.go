// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"reflect"
	"testing"

	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

func TestGroupByServer_KeepsConfigOrderWithinAServer(t *testing.T) {
	entry := func(url string) pendingMCPEntry {
		cfg, err := mcppkg.ParseToolsetConfig(mcpEntry(url, nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		return pendingMCPEntry{cfg: cfg}
	}
	pending := []pendingMCPEntry{entry("http://a/mcp"), entry("http://b/mcp"), entry("http://a/mcp"), entry("http://b/mcp"), entry("http://c/mcp")}

	got := groupByServer(pending)

	want := [][]int{{0, 2}, {1, 3}, {4}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}
