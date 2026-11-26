// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"
)

func TestProbeOpenAPISpecURL_MalformedURLSaysSo(t *testing.T) {
	warnings, err := probeOpenAPISpecURL(t.Context(), discardLogger(),
		map[string]any{"specification_url": "https://ex ample.com/spec.json?sig=" + secretToken}, false)
	if err != nil {
		t.Fatalf("probe must never block: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "the URL is not valid") {
		t.Fatalf("warnings = %v, want one warning saying the URL is not valid", warnings)
	}
	if strings.Contains(warnings[0], secretToken) {
		t.Errorf("warning carries the query: %q", warnings[0])
	}
}
