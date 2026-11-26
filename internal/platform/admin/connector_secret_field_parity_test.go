// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"slices"
	"sort"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func connectorSecretFieldLabels(t *testing.T) map[string][]string {
	t.Helper()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("LoadConnectorTypeDefinitions: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("no connector type definitions loaded")
	}
	secrets := map[string][]string{}
	for _, def := range defs {
		for _, st := range def.Subtypes {
			for _, f := range st.FieldSchema {
				if !f.Secret {
					continue
				}
				if !slices.Contains(secrets[f.Name], f.Label) {
					secrets[f.Name] = append(secrets[f.Name], f.Label)
				}
			}
		}
	}
	return secrets
}

func sortedNames[V any](set map[string]V) []string {
	names := slices.Collect(maps.Keys(set))
	sort.Strings(names)
	return names
}

func platformSecretFieldListed(name string) bool {
	_, listed := tools.PlatformSecretFieldLabels[name]
	return listed
}

func TestPlatformSecretFieldLabelsMatchConnectorSchemas(t *testing.T) {
	fromSchemas := connectorSecretFieldLabels(t)

	for _, name := range sortedNames(fromSchemas) {
		if !platformSecretFieldListed(name) {
			t.Errorf("connector schemas mark %q as secret:true but tools.PlatformSecretFieldLabels omits it — "+
				"ask_user_question will offer it in an unmasked text box. Add it to pkg/tools/platform_secret_fields.go.", name)
		}
	}
	for _, name := range sortedNames(tools.PlatformSecretFieldLabels) {
		if _, inSchemas := fromSchemas[name]; !inSchemas {
			t.Errorf("tools.PlatformSecretFieldLabels lists %q but no connector schema marks it secret:true — "+
				"a stale entry refuses legitimate questions. Remove it from pkg/tools/platform_secret_fields.go.", name)
		}
	}
}

func TestPlatformSecretFieldLabelsMatchConnectorSchemaLabels(t *testing.T) {
	fromSchemas := connectorSecretFieldLabels(t)

	for _, name := range sortedNames(fromSchemas) {
		listed, present := tools.PlatformSecretFieldLabels[name]
		if !present {
			continue
		}
		for _, label := range fromSchemas[name] {
			if !slices.Contains(listed, label) {
				t.Errorf("connector schemas draw secret field %q under the label %q but "+
					"tools.PlatformSecretFieldLabels lists %q — ask_user_question derives the phrases it "+
					"matches questions against from those labels, so a question using the schema's wording "+
					"is no longer recognised. Update pkg/tools/platform_secret_fields.go.", name, label, listed)
			}
		}
	}
	for _, name := range sortedNames(tools.PlatformSecretFieldLabels) {
		inSchemas, present := fromSchemas[name]
		if !present {
			continue
		}
		for _, label := range tools.PlatformSecretFieldLabels[name] {
			if !slices.Contains(inSchemas, label) {
				t.Errorf("tools.PlatformSecretFieldLabels gives secret field %q the label %q, which no "+
					"connector schema uses (schemas say %q) — the guard is matching wording the "+
					"Connectors page never shows. Update pkg/tools/platform_secret_fields.go.", name, label, inSchemas)
			}
		}
	}
}

func TestEveryConnectorSecretFieldHasALabel(t *testing.T) {
	for _, name := range sortedNames(tools.PlatformSecretFieldLabels) {
		labels := tools.PlatformSecretFieldLabels[name]
		if len(labels) == 0 {
			t.Errorf("secret field %q carries no label; ask_user_question can only match it by its identifier", name)
		}
		for _, label := range labels {
			if label == "" {
				t.Errorf("secret field %q carries an empty label", name)
			}
		}
	}
}

func TestNoConnectorFieldNameIsSecretInOneTypeAndPlainInAnother(t *testing.T) {
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("LoadConnectorTypeDefinitions: %v", err)
	}
	secretIn := map[string]string{}
	plainIn := map[string]string{}
	for _, def := range defs {
		for _, st := range def.Subtypes {
			for _, f := range st.FieldSchema {
				where := def.Type + "/" + st.Subtype
				if f.Secret {
					secretIn[f.Name] = where
				} else {
					plainIn[f.Name] = where
				}
			}
		}
	}
	for _, name := range sortedNames(connectorSecretFieldLabels(t)) {
		if where, plain := plainIn[name]; plain {
			t.Errorf("field %q is secret:true in %s but plain in %s — a flat name set can no longer "+
				"decide it; pkg/tools needs the connector type to disambiguate.", name, secretIn[name], where)
		}
	}
}

func TestPlatformSecretFieldLabelsExcludesPlainConnectionFields(t *testing.T) {
	for _, name := range []string{"hostname", "username", "database", "port"} {
		if platformSecretFieldListed(name) {
			t.Errorf("%q is listed as a platform secret; a question asking for it would be refused "+
				"even though the Connectors page renders it as a plain input", name)
		}
	}
}
