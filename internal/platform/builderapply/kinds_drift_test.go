// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
)

func TestKindsMatchReconcile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		local         generic.Kind
		compareProtos bool
		reconcileName string
	}{
		{local: skillKind, compareProtos: false, reconcileName: "skill"},
		{local: connectorKind, compareProtos: false, reconcileName: "connector"},
		{local: agentKind, compareProtos: false, reconcileName: "agent"},
		{local: gatewayKind, compareProtos: false, reconcileName: "entrypoint"},
		{local: workflowKind, compareProtos: false, reconcileName: "workflow"},
	}

	for _, tc := range cases {
		t.Run(tc.reconcileName, func(t *testing.T) {
			want, ok := generic.Lookup(tc.reconcileName)
			if !ok {
				t.Fatalf("reconcile does not register kind %q; if it was renamed, update kinds.go to match", tc.reconcileName)
			}
			if tc.local.Name != want.Name {
				t.Errorf("Name = %q, reconcile has %q", tc.local.Name, want.Name)
			}
			if tc.local.Plural != want.Plural {
				t.Errorf("Plural = %q, reconcile has %q", tc.local.Plural, want.Plural)
			}
			if tc.local.Order != want.Order {
				t.Errorf("Order = %d, reconcile has %d — the two apply paths would sequence this kind differently", tc.local.Order, want.Order)
			}
			if !tc.compareProtos {
				return
			}
			for _, p := range []struct {
				what        string
				local, want any
			}{
				{"CreateProto", tc.local.CreateProto, want.CreateProto},
				{"UpdateProto", tc.local.UpdateProto, want.UpdateProto},
				{"ResponseProto", tc.local.ResponseProto, want.ResponseProto},
			} {
				if lt, wt := reflect.TypeOf(p.local), reflect.TypeOf(p.want); lt != wt {
					t.Errorf("%s = %v, reconcile has %v", p.what, lt, wt)
				}
			}
		})
	}
}

func TestWorkflowHashViewMatchesReconcile(t *testing.T) {
	t.Parallel()
	want, ok := generic.Lookup("workflow")
	if !ok {
		t.Fatal("reconcile does not register the workflow kind")
	}
	wantType := reflect.TypeOf(want.CreateProto)
	localType := reflect.TypeOf(workflowKind.CreateProto)

	if got, exp := localType.NumField(), wantType.NumField(); got != exp {
		t.Fatalf("workflowHashView has %d fields, reconcile's has %d", got, exp)
	}
	for i := range localType.NumField() {
		lf, wf := localType.Field(i), wantType.Field(i)
		if lf.Name != wf.Name || lf.Type != wf.Type {
			t.Errorf("field %d: local %s %v, reconcile %s %v", i, lf.Name, lf.Type, wf.Name, wf.Type)
		}
		if lf.Tag != wf.Tag {
			t.Errorf("field %s: tag %q, reconcile has %q", lf.Name, lf.Tag, wf.Tag)
		}
	}
}

func TestA2AAgentKindHasNoReconcileCounterpart(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a2aAgent", "remoteAgent", "a2a_agent"} {
		if _, ok := generic.Lookup(name); ok {
			t.Fatalf("reconcile now registers %q; align a2aAgentKind.Order (currently %d) with it", name, a2aAgentKind.Order)
		}
	}
}

func TestComponentKindsAreOrdered(t *testing.T) {
	t.Parallel()
	ordered := componentKindsInOrder()
	prev := -1
	for _, name := range ordered {
		k, ok := kindForComponent(name)
		if !ok {
			t.Fatalf("componentKindsInOrder lists %q but kindForComponent does not resolve it", name)
		}
		if k.Order <= prev {
			t.Errorf("kind %q has Order %d, not greater than the preceding kind's %d", name, k.Order, prev)
		}
		prev = k.Order
	}
}

func TestHashedFieldsMatchReconcile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		local         generic.Kind
		reconcileName string
		renamed       map[string]string
		matchKeys     []string
	}{
		{local: agentKind, reconcileName: "agent", renamed: map[string]string{"connectorIds": "connectors"}},
		{local: gatewayKind, reconcileName: "entrypoint", renamed: map[string]string{"entrypointType": "type"}},
		{local: connectorKind, reconcileName: "connector", renamed: map[string]string{"connectorType": "type", "connectorSubtype": "subtype"}},
		{local: skillKind, reconcileName: "skill", matchKeys: []string{"name"}},
	}
	for _, tc := range cases {
		t.Run(tc.reconcileName, func(t *testing.T) {
			want, ok := generic.Lookup(tc.reconcileName)
			if !ok {
				t.Fatalf("reconcile does not register kind %q", tc.reconcileName)
			}
			for _, p := range []struct {
				what        string
				local, want any
				response    bool
			}{
				{"CreateProto", tc.local.CreateProto, want.CreateProto, false},
				{"ResponseProto", tc.local.ResponseProto, want.ResponseProto, true},
			} {
				got := hashedFields(reflect.TypeOf(p.local), p.response, nil, "")
				exp := hashedFields(reflect.TypeOf(p.want), p.response, tc.renamed, "")
				if !p.response {
					for _, k := range tc.matchKeys {
						delete(exp, k)
					}
				}
				for key, tags := range exp {
					if local, ok := got[key]; !ok {
						t.Errorf("%s: reconcile hashes %q, which %v does not", p.what, key, reflect.TypeOf(p.local))
					} else if local != tags {
						t.Errorf("%s: %q hashes with tags %q here but %q in reconcile", p.what, key, local, tags)
					}
				}
				for key := range got {
					if _, ok := exp[key]; !ok {
						t.Errorf("%s: %v hashes %q, which reconcile does not", p.what, reflect.TypeOf(p.local), key)
					}
				}
			}
		})
	}
}

func hashedFields(typ reflect.Type, response bool, renamed map[string]string, prefix string) map[string]string {
	out := map[string]string{}
	for sf := range typ.Fields() {
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if !sf.IsExported() || name == "" || name == "-" {
			continue
		}
		var key string
		var hashTags []string
		skip := false
		for tag := range strings.SplitSeq(sf.Tag.Get("cfgapply"), ",") {
			k, v, _ := strings.Cut(strings.TrimSpace(tag), "=")
			switch k {
			case "read-only", "omit-hash":
				skip = true
			case "key":
				key = v
			case "secret", "default":
				hashTags = append(hashTags, strings.TrimSpace(tag))
			}
		}
		if skip {
			continue
		}
		if response || key == "" {
			key = name
		}
		if to, ok := renamed[key]; ok {
			key = to
		}
		key = prefix + key
		slices.Sort(hashTags)
		out[key] = strings.Join(hashTags, ",")
		if nested, ok := nestedStruct(sf.Type); ok {
			maps.Copy(out, hashedFields(nested, true, nil, key+"."))
		}
	}
	return out
}

func nestedStruct(t reflect.Type) (reflect.Type, bool) {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			t = t.Elem()
			continue
		case reflect.Struct:
		default:
			return nil, false
		}
		marshaler := reflect.TypeFor[json.Marshaler]()
		if t.Implements(marshaler) || reflect.PointerTo(t).Implements(marshaler) {
			return nil, false
		}
		return t, true
	}
}
