// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"strings"
	"testing"
)

func TestEvaluateCondition_NamedNodeErrorListsItsFields(t *testing.T) {
	outputs := map[string]map[string]any{
		"fetch": {"output": map[string]any{"filename": "a.json", "version": 1}},
	}
	_, _, err := evaluateCondition(
		`'{{fetch.output.output_filename}}' == 'x'`, outputs, schemaDerived{"fetch": true})
	if err == nil {
		t.Fatal("expected an unresolved-reference error")
	}
	if !strings.Contains(err.Error(), "available fields: filename, version") {
		t.Errorf("error does not list the declared fields: %v", err)
	}
}

func TestEvaluateCondition_UnnamedNodeErrorWithholdsItsFields(t *testing.T) {
	outputs := map[string]map[string]any{
		"classify": {"output": map[string]any{
			"alice@example.com": 1,
			"bob@example.com":   2,
		}},
	}
	_, _, err := evaluateCondition(`'{{classify.output.category}}' == 'x'`, outputs, nil)
	if err == nil {
		t.Fatal("expected an unresolved-reference error")
	}
	msg := err.Error()
	for _, leaked := range []string{"alice@example.com", "bob@example.com", "available fields"} {
		if strings.Contains(msg, leaked) {
			t.Errorf("error leaked %q from a node with no declared schema: %v", leaked, err)
		}
	}
	if !strings.Contains(msg, "2 fields") || !strings.Contains(msg, "the tool") {
		t.Errorf("error gives the operator nothing to act on: %v", err)
	}
}
