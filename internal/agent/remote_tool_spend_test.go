// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestARemoteToolCallForwardsEveryAttributionFieldTheWireCarries(t *testing.T) {
	var attribution llm.SpendAttribution
	av := reflect.ValueOf(&attribution).Elem()
	for i := range av.NumField() {
		if av.Field(i).Kind() == reflect.String {
			av.Field(i).SetString(av.Type().Field(i).Name)
		}
	}

	wire := reflect.ValueOf(RemoteToolSpend(&attribution)).Elem()

	for i := range wire.NumField() {
		name := wire.Type().Field(i).Name
		if got := wire.Field(i).String(); got != name {
			t.Errorf("forwarded %s = %q, want the attribution's %q", name, got, name)
		}
	}
}
