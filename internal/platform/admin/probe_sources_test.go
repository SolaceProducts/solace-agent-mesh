// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var storedValueReaders = map[string][]string{
	"GetValuesCleartext(": {
		"connector_service.go",
		"gateway_service.go",
		"secrets_diff_handler.go",
		"secrets_export.go",
	},
	"resolveProbeValues(": {
		"gateway_service.go",
		"whatsapp_message_templates.go",
	},
	"bindProbeValues(": {
		"probe_destination.go",
		"connector_service.go",
		"gateway_service.go",
	},
}

func TestEveryReaderOfStoredValuesForARequestIsClassified(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for primitive, allowed := range storedValueReaders {
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			source, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(source), primitive) && !slices.Contains(allowed, file) {
				t.Errorf("%s uses %s, which hands saved values to a request, so a new caller needs a decision: bind it with bindProbeValues, add it to the route table in probe_routes_test.go, and list it in storedValueReaders", file, primitive)
			}
		}
	}
}
