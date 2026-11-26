// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"bytes"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/typeschema"
)

func TestApplicableFieldExamples_SelectFollowsSchemaDefault(t *testing.T) {
	fields := []typeschema.TypeFieldSchema{
		{
			Name:     "authMode",
			Type:     "select",
			Required: true,
			Default:  "hmac",
			OneOf:    []typeschema.TypeFieldOption{{Value: "token"}, {Value: "hmac"}},
		},
		{Name: "tokenName", Type: "string", Required: true, ShownWhen: map[string]any{"authMode": "token"}},
		{Name: "signatureHeader", Type: "string", Required: true, ShownWhen: map[string]any{"authMode": "hmac"}},
	}
	var buf bytes.Buffer
	writeApplicableFieldExamples(&buf, fields, "hook", "HOOK")
	got := buf.String()

	if !strings.Contains(got, `authMode: "hmac"`) {
		t.Errorf("example should pick the schema default, got:\n%s", got)
	}
	if !strings.Contains(got, "signatureHeader:") || strings.Contains(got, "tokenName:") {
		t.Errorf("example should carry only the fields the default mode accepts, got:\n%s", got)
	}
}

func TestApplicableFieldExamples_SelectIgnoresDefaultOutsideOptions(t *testing.T) {
	f := typeschema.TypeFieldSchema{
		Name:     "mode",
		Type:     "select",
		Required: true,
		Default:  "retired",
		OneOf:    []typeschema.TypeFieldOption{{Value: "a"}, {Value: "b"}},
	}
	var buf bytes.Buffer
	writeApplicableFieldExamples(&buf, []typeschema.TypeFieldSchema{f}, "x", "X")
	if !strings.Contains(buf.String(), `mode: "a"`) {
		t.Errorf("a default that is not an option must fall back to the first option, got:\n%s", buf.String())
	}
}

func TestTypeFieldsTable_QualifiesConditionalRequirement(t *testing.T) {
	fields := []typeschema.TypeFieldSchema{
		{Name: "always", Type: "string", Required: true},
		{Name: "tokenName", Type: "string", Required: true, ShownWhen: map[string]any{
			"authMode":   "token",
			"tokenPlace": []any{"header", "query"},
		}},
	}
	var buf bytes.Buffer
	renderTypeFieldsTable(&buf, fields)
	got := buf.String()

	if !strings.Contains(got, "| `always` | `string` | yes |") {
		t.Errorf("unconditional field should read plain yes, got:\n%s", got)
	}
	want := "yes, when `authMode`=`token` and `tokenPlace`=`header` or `query`"
	if !strings.Contains(got, want) {
		t.Errorf("conditional field should read %q, got:\n%s", want, got)
	}
}
