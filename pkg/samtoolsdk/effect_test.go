// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaJSON_CarriesDeclaredEffect(t *testing.T) {
	t.Parallel()
	reader := NewTool("lookup", "Looks things up", greetHandler, WithEffect(EffectReadOnly))
	writer := NewTool("update", "Updates things", greetHandler, WithEffect(EffectSideEffecting))
	silent := NewTool("silent", "Declares nothing", greetHandler)

	data, err := SchemaJSON([]*ToolDef{reader, writer, silent}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Tools map[string]map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if got := raw.Tools["lookup"]["effect"]; got != "read_only" {
		t.Errorf("lookup effect = %v, want read_only", got)
	}
	if got := raw.Tools["update"]["effect"]; got != "side_effecting" {
		t.Errorf("update effect = %v, want side_effecting", got)
	}
	if _, present := raw.Tools["silent"]["effect"]; present {
		t.Errorf("an undeclared tool must omit effect, got %v", raw.Tools["silent"]["effect"])
	}
}

func TestNewTool_RejectsUnknownEffect(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), `declares effect "readonly"`) {
			t.Fatalf("recover() = %v, want a panic naming the bad effect", r)
		}
	}()
	NewTool("bad", "Bad effect", greetHandler, WithEffect("readonly"))
}
