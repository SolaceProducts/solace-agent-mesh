// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"strings"
	"testing"
)

func TestEmitYAMLDropsEmptyHeaderFields(t *testing.T) {
	d := resourceDoc{
		Kind: "model",
		Name: "planning",
		Spec: map[string]any{"provider": "openai"},
	}
	raw, err := emitYAML(d)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	out := string(raw)
	if strings.Contains(out, "description:") {
		t.Errorf("expected omitempty to drop description: %s", out)
	}
	if !strings.Contains(out, "kind: model") {
		t.Errorf("missing kind: %s", out)
	}
}

func TestDropEmptyKeepsBoolAndNumberZeros(t *testing.T) {
	in := map[string]any{
		"flag":   false,
		"count":  0,
		"name":   "x",
		"empty":  "",
		"nilv":   nil,
		"empSli": []any{},
		"empMap": map[string]any{},
	}
	out := dropEmpty(in)
	if _, ok := out["flag"]; !ok {
		t.Errorf("dropEmpty dropped a bool zero")
	}
	if _, ok := out["count"]; !ok {
		t.Errorf("dropEmpty dropped a numeric zero")
	}
	if _, ok := out["empty"]; ok {
		t.Errorf("dropEmpty kept empty string")
	}
	if _, ok := out["nilv"]; ok {
		t.Errorf("dropEmpty kept nil")
	}
	if _, ok := out["empSli"]; ok {
		t.Errorf("dropEmpty kept empty slice")
	}
	if _, ok := out["empMap"]; ok {
		t.Errorf("dropEmpty kept empty map")
	}
}

func TestTakeStringMissingKey(t *testing.T) {
	m := map[string]any{"name": "x"}
	got, ok := takeString(m, "missing")
	if ok || got != "" {
		t.Errorf("takeString(missing) = (%q, %v)", got, ok)
	}
	if _, present := m["name"]; !present {
		t.Errorf("takeString must not touch other keys")
	}
}

func TestTakeStringWrongTypePutsBack(t *testing.T) {
	m := map[string]any{"name": 42}
	got, ok := takeString(m, "name")
	if ok || got != "" {
		t.Errorf("takeString returned (%q, %v); want empty + false", got, ok)
	}
	if v, present := m["name"]; !present || v != 42 {
		t.Errorf("takeString must put non-string back; got %+v present=%v", v, present)
	}
}
