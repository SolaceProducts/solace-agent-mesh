// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"strings"
	"testing"
)

func TestConnectorDesignSkill_PointsAtTheLiveTypeList(t *testing.T) {
	for path, body := range credentialCorpusFiles(t) {
		if !strings.Contains(path, "sam-connector-design") {
			continue
		}
		for _, want := range []string{
			"platform_list_components",
			"component_type: connector_type",
			"never propose a connector type it does not list",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not say %q: the static type list would read as the authority again", path, want)
			}
		}
		return
	}
	t.Fatal("sam-connector-design was not in the corpus; the walk is broken")
}
