// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

var setByTheSTR = map[string]bool{"Surface": true, "ToolName": true, "ModelKey": true, "ModelConfigID": true, "AvoidedInputBytes": true}

func TestToolCallSpendCarriesEveryFieldTheInvocationForwarded(t *testing.T) {
	var wire remotetool.SpendAttribution
	wv := reflect.ValueOf(&wire).Elem()
	for i := range wv.NumField() {
		wv.Field(i).SetString(wv.Type().Field(i).Name)
	}

	got := toolCallSpend(&wire, "the-tool", "the-alias", "the-config")

	gv := reflect.ValueOf(got).Elem()
	for i := range gv.NumField() {
		name := gv.Type().Field(i).Name
		if gv.Field(i).Kind() != reflect.String {
			if !gv.Field(i).IsNil() {
				t.Errorf("%s = %v, want none: a tool's call withholds nothing", name, gv.Field(i).Elem())
			}
			continue
		}
		want := name
		switch name {
		case "Surface":
			want = "tool"
		case "ToolName":
			want = "the-tool"
		case "ModelKey":
			want = "the-alias"
		case "ModelConfigID":
			want = "the-config"
		}
		if got := gv.Field(i).String(); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestTheInvokeWireCarriesEveryAttributionFieldTheSTRDoesNotSet(t *testing.T) {
	wireFields := map[string]bool{}
	for f := range reflect.TypeFor[remotetool.SpendAttribution]().Fields() {
		wireFields[f.Name] = true
	}
	for f := range reflect.TypeFor[llm.SpendAttribution]().Fields() {
		if !setByTheSTR[f.Name] && !wireFields[f.Name] {
			t.Errorf("llm.SpendAttribution.%s has no remotetool.SpendAttribution field, so the STR drops it", f.Name)
		}
		delete(wireFields, f.Name)
	}
	for name := range wireFields {
		t.Errorf("remotetool.SpendAttribution.%s has no llm.SpendAttribution field", name)
	}
}
