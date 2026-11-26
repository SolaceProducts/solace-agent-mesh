// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typeschema

import (
	"slices"
	"strings"
	"testing"
)

func TestCustomTypes_HasEventRules(t *testing.T) {
	got := CustomTypeNames()
	if !slices.Contains(got, "event_rules") {
		t.Fatalf("custom types missing event_rules (got %v)", got)
	}
}

func TestLookupCustomType_EventRulesShape(t *testing.T) {
	c, ok := LookupCustomType("event_rules")
	if !ok {
		t.Fatal("LookupCustomType(event_rules) not found")
	}
	if c.Type != "event_rules" {
		t.Errorf("Type = %q; want event_rules", c.Type)
	}
	if c.ItemKind != "array<object>" {
		t.Errorf("ItemKind = %q; want array<object>", c.ItemKind)
	}
	if c.Description == "" {
		t.Error("Description should not be empty")
	}
	if c.Example == "" {
		t.Error("Example should not be empty (driven by sam config schema example)")
	}

	required := map[string]bool{}
	all := map[string]TypeFieldSchema{}
	for _, f := range c.Fields {
		all[f.Name] = f
		if f.Required {
			required[f.Name] = true
		}
	}
	if !required["name"] {
		t.Errorf("event_rules.name should be Required")
	}
	if required["subscriptions"] {
		t.Errorf("event_rules.subscriptions must no longer be unconditionally Required; the existingQueueName/subscriptions mutex owns that invariant")
	}
	for _, want := range []string{
		"targetAgent", "targetWorkflowName", "promptTemplate",
		"acknowledgmentPolicy", "successOutput", "errorOutput",
		"messageFormat", "defaultUserIdentity", "userIdentityExpression",
		"forwardContext", "structuredInvocation",
	} {
		if _, ok := all[want]; !ok {
			t.Errorf("event_rules schema missing optional field %q", want)
		}
	}
	for _, want := range []string{
		"inputExpression", "payloadEncoding", "artifactProcessing", "existingQueueName", "eventFilter",
	} {
		f, ok := all[want]
		if !ok {
			t.Errorf("event_rules schema missing advanced field %q", want)
			continue
		}
		if !strings.Contains(f.Hint, "[advanced]") {
			t.Errorf("advanced field %q should carry [advanced] hint; got %q", want, f.Hint)
		}
	}

	if got := all["name"].Pattern; got != `^[a-zA-Z0-9_-]+$` {
		t.Errorf("name.Pattern = %q; want validator's rule-name regex", got)
	}

	wantMessageFormats := []string{"json", "text", "xml", "raw_bytes", "protobuf", "structured"}
	var gotMsgFmt []string
	for _, o := range all["messageFormat"].OneOf {
		gotMsgFmt = append(gotMsgFmt, o.Value)
	}
	if !slices.Equal(gotMsgFmt, wantMessageFormats) {
		t.Errorf("messageFormat.OneOf = %v; want %v", gotMsgFmt, wantMessageFormats)
	}

	if _, ok := all["acknowledgmentPolicy"]; !ok {
		t.Error("missing field acknowledgmentPolicy")
	}
}

func TestCustomTypes_HasOperations(t *testing.T) {
	got := CustomTypeNames()
	if !slices.Contains(got, "operations") {
		t.Fatalf("custom types missing operations (got %v)", got)
	}
}

func TestLookupCustomType_OperationsShape(t *testing.T) {
	c, ok := LookupCustomType("operations")
	if !ok {
		t.Fatal("LookupCustomType(operations) not found")
	}
	if c.Type != "operations" {
		t.Errorf("Type = %q; want operations", c.Type)
	}
	if c.ItemKind != "array<object>" {
		t.Errorf("ItemKind = %q; want array<object>", c.ItemKind)
	}
	if c.Description == "" {
		t.Error("Description should not be empty")
	}
	if c.Example == "" {
		t.Error("Example should not be empty (drives sam config schema example)")
	}

	required := map[string]bool{}
	all := map[string]TypeFieldSchema{}
	for _, f := range c.Fields {
		all[f.Name] = f
		if f.Required {
			required[f.Name] = true
		}
	}
	for _, want := range []string{"tool_name", "tool_description", "topic", "wait_for_response", "payload_format", "input_schema"} {
		if !required[want] {
			t.Errorf("operations.%s should be Required", want)
		}
	}
	for _, want := range []string{"qos", "request_expiry_ms", "reply_mode"} {
		if _, ok := all[want]; !ok {
			t.Errorf("operations schema missing optional field %q", want)
		}
	}
	for _, want := range []string{"request_expiry_ms", "reply_mode"} {
		if !strings.Contains(all[want].Hint, "[advanced]") {
			t.Errorf("advanced field %q should carry [advanced] hint; got %q", want, all[want].Hint)
		}
	}
}

func TestOperations_ExampleShape(t *testing.T) {
	c, _ := LookupCustomType("operations")
	for _, want := range []string{"tool_name:", "topic:", "input_schema:", "x-solace-payload-path:", "{store_id}", "create_replenishment_command"} {
		if !strings.Contains(c.Example, want) {
			t.Errorf("operations.Example missing %q:\n%s", want, c.Example)
		}
	}
	if strings.Contains(c.Example, "{{") {
		t.Errorf("operations.Example uses legacy {{ }} syntax; expected single-brace {var}:\n%s", c.Example)
	}
}

func TestLookupCustomType_Unknown(t *testing.T) {
	_, ok := LookupCustomType("definitely-not-a-registered-type")
	if ok {
		t.Error("LookupCustomType returned ok=true for an unregistered token")
	}
}

func TestEventRules_ExampleShape(t *testing.T) {
	c, _ := LookupCustomType("event_rules")
	for _, want := range []string{"name:", "subscriptions:", "topic:"} {
		if !strings.Contains(c.Example, want) {
			t.Errorf("event_rules.Example missing %q:\n%s", want, c.Example)
		}
	}
}
