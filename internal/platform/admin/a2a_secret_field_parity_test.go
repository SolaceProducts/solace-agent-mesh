// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func a2aAgentSecretFieldListed(name string) bool {
	_, listed := tools.A2AAgentSecretFieldLabels[name]
	return listed
}

func declaredAuthTypes(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}
	fset := token.NewFileSet()
	var types []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		types = append(types, authTypeConstValues(t, file)...)
	}
	if len(types) == 0 {
		t.Fatal("no authType* constants found; this scan can no longer see the auth types it guards")
	}
	slices.Sort(types)
	return types
}

func authTypeConstValues(t *testing.T, file *ast.File) []string {
	t.Helper()
	var values []string
	for _, decl := range file.Decls {
		gen, isGen := decl.(*ast.GenDecl)
		if !isGen || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue {
				continue
			}
			for i, name := range value.Names {
				if !strings.HasPrefix(name.Name, "authType") || i >= len(value.Values) {
					continue
				}
				lit, isLit := value.Values[i].(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					t.Fatalf("const %s is not a string literal; the auth-type scan cannot read it", name.Name)
				}
				unquoted, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					t.Fatalf("const %s has an unreadable value %s: %v", name.Name, lit.Value, unquoteErr)
				}
				values = append(values, unquoted)
			}
		}
	}
	return values
}

func TestA2AAgentSecretFieldLabelsMatchAuthSecretPairs(t *testing.T) {
	fromPairs := map[string]bool{}
	for _, authType := range declaredAuthTypes(t) {
		for _, pair := range authSecretPairs(authType) {
			fromPairs[pair.wire] = true
		}
	}
	if len(fromPairs) == 0 {
		t.Fatal("authSecretPairs declared no secret field for any auth type")
	}

	for _, name := range sortedNames(fromPairs) {
		if !a2aAgentSecretFieldListed(name) {
			t.Errorf("authSecretPairs declares %q as a remote A2A agent secret but "+
				"tools.A2AAgentSecretFieldLabels omits it — ask_user_question will offer it in an "+
				"unmasked text box. Add it to pkg/tools/platform_secret_fields.go.", name)
		}
	}
	for _, name := range sortedNames(tools.A2AAgentSecretFieldLabels) {
		if !fromPairs[name] {
			t.Errorf("tools.A2AAgentSecretFieldLabels lists %q but no auth type declares it in "+
				"authSecretPairs — a stale entry refuses legitimate questions. Remove it from "+
				"pkg/tools/platform_secret_fields.go.", name)
		}
	}
}

func TestEveryA2AAuthTypeInTheSchemaIsDeclaredHere(t *testing.T) {
	declared := declaredAuthTypes(t)
	for _, authType := range a2aSchemaAuthTypes(t) {
		if authType == "none" {
			continue
		}
		if !slices.Contains(declared, authType) {
			t.Errorf("the a2a-agent schema accepts auth type %q but this package declares no "+
				"authType constant for it; authSecretPairs treats it as unknown and reports no "+
				"secret fields for it", authType)
		}
	}
}

func a2aSchemaAuthTypes(t *testing.T) []string {
	t.Helper()
	raw, err := schema.SchemaContent("a2a-agent")
	if err != nil {
		t.Fatalf("SchemaContent(a2a-agent): %v", err)
	}
	var doc struct {
		Defs struct {
			AuthConfig struct {
				Properties struct {
					Type struct {
						Enum []string `json:"enum"`
					} `json:"type"`
				} `json:"properties"`
			} `json:"authConfig"`
		} `json:"$defs"`
	}
	if unmarshalErr := json.Unmarshal(raw, &doc); unmarshalErr != nil {
		t.Fatalf("parsing a2a-agent schema: %v", unmarshalErr)
	}
	enum := doc.Defs.AuthConfig.Properties.Type.Enum
	if len(enum) == 0 {
		t.Fatal("the a2a-agent schema declares no auth-type enum")
	}
	return enum
}

func TestA2AAgentSecretFieldLabelsExcludePlainFields(t *testing.T) {
	for _, name := range []string{"hostname", "username", "database", "port", "clientId", "tokenEndpoint", "headerName"} {
		if a2aAgentSecretFieldListed(name) {
			t.Errorf("%q is listed as a remote A2A agent secret; a question asking for it would be "+
				"refused even though it is a plain input", name)
		}
		if platformSecretFieldListed(name) {
			t.Errorf("%q is listed as a connector secret; a question asking for it would be refused "+
				"even though it is a plain input", name)
		}
	}
}
