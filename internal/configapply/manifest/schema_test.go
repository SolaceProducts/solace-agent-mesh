// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package manifest

import (
	"strings"
	"testing"
)

var schemaOnlyKeys = map[string]string{
	"imports": "reserved for a future WI — accepted silently, not yet authorable",
}

func TestManifestSchemaCoversEveryTopLevelKey(t *testing.T) {
	documented := make(map[string]bool, len(ManifestSchema.Fields))
	for _, f := range ManifestSchema.Fields {
		documented[f.JSONName] = true
	}

	for key := range knownTopLevelKeys {
		if documented[key] {
			continue
		}
		if reason, ok := schemaOnlyKeys[key]; ok {
			t.Logf("key %q undocumented by design: %s", key, reason)
			continue
		}
		t.Errorf("manifest key %q is accepted by the parser but missing from ManifestSchema.Fields — add a FieldSchema for it", key)
	}

	for _, f := range ManifestSchema.Fields {
		if _, ok := knownTopLevelKeys[f.JSONName]; !ok {
			t.Errorf("ManifestSchema documents %q, which knownTopLevelKeys rejects — the schema promises a key the parser refuses", f.JSONName)
		}
	}
}

func TestManifestSchemaPlatformDocumentsBothSubBlocks(t *testing.T) {
	var desc string
	for _, f := range ManifestSchema.Fields {
		if f.JSONName == "platform" {
			desc = f.Description
		}
	}
	if desc == "" {
		t.Fatal("ManifestSchema has no platform field")
	}
	for _, want := range []string{"profileProvider", "defaultRoles", "roles"} {
		if !strings.Contains(desc, want) {
			t.Errorf("platform description must mention %q so it is discoverable offline; got %q", want, desc)
		}
	}
}
