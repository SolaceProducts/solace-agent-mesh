// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"os"
	"regexp"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/toolbind"
)

var strBinaryLiteral = regexp.MustCompile(`_str_binary"\]?\s*(?::|=)\s*"([a-z_]+)"`)

func TestConnectorRuntimesCoverEveryBinaryTheDeployEmits(t *testing.T) {
	src, err := os.ReadFile("yaml_service.go")
	if err != nil {
		t.Fatal(err)
	}
	found := strBinaryLiteral.FindAllStringSubmatch(string(src), -1)
	if len(found) < 9 {
		t.Fatalf("found only %d _str_binary literals in yaml_service.go; the scan is broken", len(found))
	}
	emitted := map[string]bool{}
	for _, m := range found {
		emitted[m[1]] = true
		if _, ok := toolbind.ConnectorRuntimes[m[1]]; !ok {
			t.Errorf("the deploy emits _str_binary %q but toolbind.ConnectorRuntimes does not know it", m[1])
		}
	}
	for binary := range toolbind.ConnectorRuntimes {
		if !emitted[binary] {
			t.Errorf("toolbind.ConnectorRuntimes lists %q, which no connector tool builder emits", binary)
		}
	}
}

func TestConnectorRuntimeTypesAreRealConnectorTypes(t *testing.T) {
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, def := range defs {
		known[def.Type] = true
	}
	for binary, runtime := range toolbind.ConnectorRuntimes {
		if !known[runtime.Type] {
			t.Errorf("%q is advised as the runtime of `type: %s` connectors, which is not a connector type the platform defines", binary, runtime.Type)
		}
	}
}
