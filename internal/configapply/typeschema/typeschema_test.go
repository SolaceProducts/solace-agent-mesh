// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typeschema

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestGatewayTypeSchemas_HidesDefaultOffTypes(t *testing.T) {
	got := GatewayTypeNames()
	for _, listed := range []string{"slack", "whatsapp"} {
		if !slices.Contains(got, listed) {
			t.Errorf("gateway types missing default-on type %q (got %v)", listed, got)
		}
	}
	for _, hidden := range []string{"email", "google_chat"} {
		if slices.Contains(got, hidden) {
			t.Errorf("gateway types must hide default-off type %q (got %v)", hidden, got)
		}
	}
}

func TestVisibleGatewaySchemas_FlagTogglesEmail(t *testing.T) {
	hasEmail := func(reg *features.Registry) bool {
		for _, s := range entplatform.VisibleGatewaySchemas(reg) {
			if s.Type == "email" {
				return true
			}
		}
		return false
	}

	t.Setenv("SAM_FEATURE_EMAIL_GATEWAY", "false")
	off := features.NewRegistry()
	if err := features.LoadFlags(off); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	if hasEmail(off) {
		t.Error("email entrypoint must be hidden when SAM_FEATURE_EMAIL_GATEWAY is off")
	}

	t.Setenv("SAM_FEATURE_EMAIL_GATEWAY", "true")
	on := features.NewRegistry()
	if err := features.LoadFlags(on); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	if !hasEmail(on) {
		t.Error("email entrypoint must be visible when SAM_FEATURE_EMAIL_GATEWAY is on")
	}
}

func TestConnectorTypeSchemas_HidesUnreleasedTypes(t *testing.T) {
	got := ConnectorTypeNames()
	if !slices.Contains(got, "sql") {
		t.Errorf("connector types missing released type %q (got %v)", "sql", got)
	}
	if slices.Contains(got, "email") {
		t.Errorf("connector types must hide unreleased type %q (got %v)", "email", got)
	}
}

func TestVisibleConnectorSchemas_FlagTogglesEmail(t *testing.T) {
	hasEmail := func(reg *features.Registry) bool {
		for _, s := range entplatform.VisibleConnectorSchemas(reg) {
			if s.Type == "email" {
				return true
			}
		}
		return false
	}

	t.Setenv("SAM_FEATURE_EMAIL_CONNECTOR", "false")
	off := features.NewRegistry()
	if err := features.LoadFlags(off); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	if hasEmail(off) {
		t.Error("email connector must be hidden when SAM_FEATURE_EMAIL_CONNECTOR is off")
	}

	t.Setenv("SAM_FEATURE_EMAIL_CONNECTOR", "true")
	on := features.NewRegistry()
	if err := features.LoadFlags(on); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	if !hasEmail(on) {
		t.Error("email connector must be visible when SAM_FEATURE_EMAIL_CONNECTOR is on")
	}
}

func TestGatewayTypeSchema_SlackHasFields(t *testing.T) {
	s, err := LookupGatewayType("slack")
	if err != nil {
		t.Fatalf("lookup slack: %v", err)
	}
	wantSecret := map[string]bool{"bot_token": false, "app_token": false}
	for _, f := range s.Fields {
		if _, ok := wantSecret[f.Name]; ok {
			wantSecret[f.Name] = true
			if !f.Secret {
				t.Errorf("slack field %q should be Secret", f.Name)
			}
		}
	}
	for name, found := range wantSecret {
		if !found {
			t.Errorf("slack schema missing required field %q", name)
		}
	}
}

func TestGatewayTypeSchema_FieldOrderingStable(t *testing.T) {
	s, err := LookupGatewayType("slack")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	for i := 1; i < len(s.Fields); i++ {
		prev := s.Fields[i-1]
		cur := s.Fields[i]
		if prev.Order > cur.Order {
			t.Errorf("fields not sorted by Order at index %d: %q(%v) > %q(%v)",
				i, prev.Name, prev.Order, cur.Name, cur.Order)
		}
	}
}

func TestLookupGatewayType_UnknownLists(t *testing.T) {
	_, err := LookupGatewayType("nope-not-a-gateway")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "known:") {
		t.Errorf("error should list known types, got: %v", err)
	}
}

func TestConnectorTypeSchemas_PostgresHasPasswordSecret(t *testing.T) {
	pg, err := LookupConnectorType("sql", "postgres")
	if err != nil {
		t.Fatalf("lookup sql/postgres: %v", err)
	}
	if pg.Type != "sql" || pg.Subtype != "postgres" {
		t.Errorf("type/subtype: got %q/%q", pg.Type, pg.Subtype)
	}
	var sawPassword, sawDatabase bool
	for _, f := range pg.Fields {
		if f.Name == "password" {
			sawPassword = true
			if !f.Secret {
				t.Error("password should be Secret")
			}
		}
		if f.Name == "database" {
			sawDatabase = true
			if !f.Required {
				t.Error("database should be required")
			}
		}
	}
	if !sawPassword {
		t.Error("postgres schema missing password field")
	}
	if !sawDatabase {
		t.Error("postgres schema missing database field")
	}
}

func TestConnectorTypeSchemas_TypesArePresent(t *testing.T) {
	got := ConnectorTypeNames()
	if len(got) == 0 {
		t.Fatal("expected at least one connector type")
	}
	for _, want := range []string{"sql", "mcp"} {
		if !slices.Contains(got, want) {
			t.Errorf("connector types missing %q (got %v)", want, got)
		}
	}
}

func TestLookupConnectorType_DefaultsToFirstSubtype(t *testing.T) {
	got, err := LookupConnectorType("sql", "")
	if err != nil {
		t.Fatalf("lookup sql,empty: %v", err)
	}
	want := ConnectorSubtypeNames("sql")[0]
	if got.Subtype != want {
		t.Errorf("default subtype: got %q want %q", got.Subtype, want)
	}
}

func TestGatewaySchemas_ExposeAdvancedRegistryFields(t *testing.T) {
	advancedFields := map[string][]string{
		"webhook":    {"systemPurpose", "responseFormat", "runAs"},
		"mcp":        {"systemPurpose", "responseFormat", "runAs"},
		"event_mesh": {"systemPurpose", "responseFormat"},
	}
	for gwType, names := range advancedFields {
		t.Run(gwType, func(t *testing.T) {
			s, err := LookupGatewayType(gwType)
			if err != nil {
				t.Fatalf("LookupGatewayType: %v", err)
			}
			byName := map[string]TypeFieldSchema{}
			for _, f := range s.Fields {
				byName[f.Name] = f
			}
			for _, name := range names {
				f, ok := byName[name]
				if !ok {
					t.Errorf("field %q missing from the %s authoring schema", name, gwType)
					continue
				}
				if !strings.HasPrefix(f.Hint, "[advanced]") {
					t.Errorf("%s field %q hint = %q, want the [advanced] lead so authors know the UI omits it",
						gwType, name, f.Hint)
				}
			}
		})
	}
}

func TestNoZeroLengthCapsProjected(t *testing.T) {
	assertNoZeroCaps := func(t *testing.T, surface, owner string, fields []TypeFieldSchema) {
		t.Helper()
		for _, f := range fields {
			if f.MinLen == 0 || f.MaxLen == 0 {
				t.Errorf("%s %s field %q projects minLen=%d maxLen=%d; an absent cap must project as negative",
					surface, owner, f.Name, f.MinLen, f.MaxLen)
			}
		}
	}

	t.Run("gateway types", func(t *testing.T) {
		for _, s := range GatewayTypeSchemasSorted() {
			assertNoZeroCaps(t, "entrypoint", s.Type, s.Fields)
		}
	})

	t.Run("custom types", func(t *testing.T) {
		for _, name := range CustomTypeNames() {
			c, ok := LookupCustomType(name)
			if !ok {
				t.Errorf("CustomTypeNames lists %q but LookupCustomType does not resolve it", name)
				continue
			}
			assertNoZeroCaps(t, "custom type", name, c.Fields)
		}
	})
}

func TestProjectedOptionsAlwaysCarryALabel(t *testing.T) {
	checked := 0
	check := func(t *testing.T, owner string, fields []TypeFieldSchema) {
		t.Helper()
		for _, f := range fields {
			for _, opt := range f.OneOf {
				checked++
				if opt.Label == "" {
					t.Errorf("%s field %q option %q projects a blank label; it must default to the value",
						owner, f.Name, opt.Value)
				}
			}
		}
	}

	for _, s := range GatewayTypeSchemasSorted() {
		check(t, "entrypoint "+s.Type, s.Fields)
	}
	for _, name := range CustomTypeNames() {
		if c, ok := LookupCustomType(name); ok {
			check(t, "custom type "+name, c.Fields)
		}
	}

	if checked == 0 {
		t.Fatal("no enum options were projected, so this test asserted nothing")
	}
}

func TestProjectGatewayFields_CarriesConditionalDisplay(t *testing.T) {
	fields := projectGatewayFields(map[string]any{
		"password": map[string]any{
			"type":               "password",
			"required":           true,
			"conditionalDisplay": map[string]string{"auth_mode": "basic"},
		},
		"token": map[string]any{
			"type":               "string",
			"required":           true,
			"conditionalDisplay": map[string]any{"auth_mode": []any{"bearer", "oauth"}},
		},
		"host": map[string]any{"type": "string", "required": true},
	})
	byName := map[string]TypeFieldSchema{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	if got := byName["password"].ShownWhen["auth_mode"]; got != "basic" {
		t.Errorf("string-map condition: got %v, want basic", got)
	}
	if got, ok := byName["token"].ShownWhen["auth_mode"].([]any); !ok || len(got) != 2 {
		t.Errorf("list condition: got %#v", byName["token"].ShownWhen["auth_mode"])
	}
	if byName["host"].ShownWhen != nil {
		t.Errorf("unconditional field: got %v, want nil", byName["host"].ShownWhen)
	}
}

func TestProjectGatewayFields_KeepsEveryDeclaredOption(t *testing.T) {
	type declaredOption struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	declared := 0
	for _, s := range entplatform.GetAllGatewaySchemas() {
		raw, ok := s.Schema.(map[string]any)
		if !ok {
			continue
		}
		wire, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("marshal %s schema: %v", s.Type, err)
		}
		var decoded map[string]struct {
			Options []declaredOption `json:"options"`
		}
		if err := json.Unmarshal(wire, &decoded); err != nil {
			t.Fatalf("unmarshal %s schema: %v", s.Type, err)
		}
		projected := map[string][]TypeFieldOption{}
		for _, f := range projectGatewayFields(raw) {
			projected[f.Name] = f.OneOf
		}
		for name, field := range decoded {
			var want []TypeFieldOption
			for _, o := range field.Options {
				want = append(want, TypeFieldOption(o))
			}
			declared += len(want)
			if diff := cmp.Diff(want, projected[name]); diff != "" {
				t.Errorf("projectGatewayFields(%s).%s options mismatch (-declared +projected):\n%s", s.Type, name, diff)
			}
		}
	}
	if declared == 0 {
		t.Fatal("no gateway schema declared options, so this test asserted nothing")
	}
}
