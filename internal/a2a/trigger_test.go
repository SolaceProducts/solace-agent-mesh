// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"reflect"
	"testing"
)

func TestUserPropertiesTriggerRoundTrip(t *testing.T) {
	up := UserProperties{Timestamp: 1, Trigger: TriggerScheduled}
	m := EncodeUserProperties(&up)
	if m[PropTrigger] != TriggerScheduled {
		t.Fatalf("encode trigger = %q, want %q", m[PropTrigger], TriggerScheduled)
	}
	if got := DecodeUserProperties(m); got.Trigger != TriggerScheduled {
		t.Fatalf("decode trigger = %q, want %q", got.Trigger, TriggerScheduled)
	}
}

func TestUserPropertiesBlankTriggerOmitted(t *testing.T) {
	m := EncodeUserProperties(&UserProperties{Timestamp: 1})
	if _, ok := m[PropTrigger]; ok {
		t.Fatalf("blank trigger should not be encoded, got %q", m[PropTrigger])
	}
	if got := DecodeUserProperties(m); got.Trigger != "" {
		t.Fatalf("decode of absent trigger = %q, want empty", got.Trigger)
	}
}

func TestNormalizeTriggerClampsToClosedSet(t *testing.T) {
	cases := map[string]string{
		TriggerScheduled:   TriggerScheduled,
		TriggerInteractive: TriggerInteractive,
		TriggerEval:        TriggerEval,
		TriggerEvent:       TriggerEvent,
		"":                 TriggerUnknown,
		"garbage":          TriggerUnknown,
		"SCHEDULED":        TriggerUnknown,
	}
	for in, want := range cases {
		if got := NormalizeTrigger(in); got != want {
			t.Errorf("NormalizeTrigger(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAddTrigger(t *testing.T) {
	m := map[string]string{}
	AddTrigger(m, "")
	if _, ok := m[PropTrigger]; ok {
		t.Fatal("AddTrigger with empty must not write the key")
	}
	AddTrigger(m, TriggerScheduled)
	if m[PropTrigger] != TriggerScheduled {
		t.Fatalf("AddTrigger = %q, want %q", m[PropTrigger], TriggerScheduled)
	}
}

func TestUserPropertiesRootRoundTrip(t *testing.T) {
	up := UserProperties{Timestamp: 1, RootKind: RootKindWorkflow, RootID: "wf-1", RootNodeID: "node-1"}
	m := EncodeUserProperties(&up)
	want := map[string]string{PropRootKind: RootKindWorkflow, PropRootID: "wf-1", PropRootNodeID: "node-1"}
	for key, value := range want {
		if m[key] != value {
			t.Errorf("encoded %s = %q, want %q", key, m[key], value)
		}
	}
	got := DecodeUserProperties(m)
	if got.RootKind != up.RootKind || got.RootID != up.RootID || got.RootNodeID != up.RootNodeID {
		t.Errorf("decoded root = %q/%q/%q, want %q/%q/%q", got.RootKind, got.RootID, got.RootNodeID, up.RootKind, up.RootID, up.RootNodeID)
	}
}

func TestUserPropertiesBlankRootOmitted(t *testing.T) {
	m := EncodeUserProperties(&UserProperties{Timestamp: 1})
	for _, key := range []string{PropRootKind, PropRootID, PropRootNodeID} {
		if _, ok := m[key]; ok {
			t.Errorf("blank %s should not be encoded, got %q", key, m[key])
		}
	}
}

func TestAddRootSkipsBlankParts(t *testing.T) {
	m := map[string]string{}
	AddRoot(m, RootKindAgent, "agent-1", "")
	if m[PropRootKind] != RootKindAgent || m[PropRootID] != "agent-1" {
		t.Errorf("AddRoot wrote %v", m)
	}
	if _, ok := m[PropRootNodeID]; ok {
		t.Errorf("AddRoot wrote a blank node id: %v", m)
	}
}

func TestResolveEntrypointName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props UserProperties
		want  string
	}{
		{"top-level task takes the gateway's client id", UserProperties{ClientID: "webui", Origin: Origin{EntrypointName: "forged"}}, "webui"},
		{"delegated task takes the forwarded name", UserProperties{ClientID: "ignored", Origin: Origin{EntrypointName: "webui"}, DelegatingAgentName: "parent"}, "webui"},
		{"delegated task with none forwarded has none", UserProperties{ClientID: "ignored", DelegatingAgentName: "parent"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.props.ResolveEntrypointName(); got != tc.want {
				t.Errorf("ResolveEntrypointName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUserPropertiesOriginRoundTrip(t *testing.T) {
	origin := Origin{Channel: "event_mesh", UserUUID: "user-uuid", EntrypointUUID: "gw-uuid", EntrypointName: "webui"}
	m := EncodeUserProperties(&UserProperties{Timestamp: 1, Origin: origin})
	want := map[string]string{PropChannel: "event_mesh", PropUserUUID: "user-uuid", PropEntrypointUUID: "gw-uuid", PropEntrypointName: "webui"}
	for key, value := range want {
		if m[key] != value {
			t.Errorf("encoded %s = %q, want %q", key, m[key], value)
		}
	}
	if got := DecodeUserProperties(m).Origin; got != origin {
		t.Errorf("decoded origin = %+v, want %+v", got, origin)
	}
}

func TestUserPropertiesBlankOriginOmitted(t *testing.T) {
	m := EncodeUserProperties(&UserProperties{Timestamp: 1})
	for _, key := range []string{PropChannel, PropUserUUID, PropEntrypointUUID, PropEntrypointName} {
		if got, ok := m[key]; ok {
			t.Errorf("blank %s encoded as %q", key, got)
		}
	}
}

func TestDecodeOriginReadsEveryFieldAddOriginWrites(t *testing.T) {
	var origin Origin
	ov := reflect.ValueOf(&origin).Elem()
	for i := range ov.NumField() {
		ov.Field(i).SetString("v-" + ov.Type().Field(i).Name)
	}
	m := map[string]string{}
	AddOrigin(m, origin)

	if got := DecodeOrigin(m); got != origin {
		t.Errorf("DecodeOrigin = %+v, want %+v: a field AddOrigin writes is not read back", got, origin)
	}
}
