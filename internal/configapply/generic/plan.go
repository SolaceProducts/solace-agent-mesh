// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic

import (
	"fmt"
	"reflect"
	"sort"
)

type Op struct {
	Name string

	Source string

	Provenance string

	ID string

	Create reflect.Value

	Patch reflect.Value

	Actual reflect.Value
}

type KindPlan struct {
	Kind      string
	Creates   []Op
	Updates   []Op
	Unchanged []Op
	Deletes   []Op
}

type DesiredEntry struct {
	Name       string
	Source     string
	Provenance string
	Create     any
}

func Plan(desired []DesiredEntry, actual []reflect.Value, k Kind) (*KindPlan, error) {
	plan := &KindPlan{Kind: k.Name}

	desiredByName := make(map[string]DesiredEntry, len(desired))
	for _, d := range desired {
		if _, dup := desiredByName[d.Name]; dup {
			return nil, fmt.Errorf("duplicate %s name %q in resolved resources", k.Name, d.Name)
		}
		desiredByName[d.Name] = d
	}

	actualByName := make(map[string]reflect.Value, len(actual))
	for _, a := range actual {
		name := k.NameFromResponse(a.Interface())
		actualByName[name] = a
	}

	desiredNames := make([]string, 0, len(desiredByName))
	for n := range desiredByName {
		desiredNames = append(desiredNames, n)
	}
	sort.Strings(desiredNames)

	for _, name := range desiredNames {
		d := desiredByName[name]
		existing, found := actualByName[name]
		createVal := reflect.ValueOf(d.Create)
		if createVal.Kind() == reflect.Pointer {
			createVal = createVal.Elem()
		}
		if !found {
			plan.Creates = append(plan.Creates, Op{
				Name:       name,
				Source:     d.Source,
				Provenance: d.Provenance,
				Create:     createVal,
			})
			continue
		}
		redacted := RedactedKeysFromSecretField(existing.Interface())
		desiredHash, err := Hash(d.Create, redacted)
		if err != nil {
			return nil, err
		}
		actualHash, err := Hash(existing.Interface(), redacted)
		if err != nil {
			return nil, err
		}
		id := k.IDFromResponse(existing.Interface())
		unchanged := Op{
			Name:       name,
			Source:     d.Source,
			Provenance: d.Provenance,
			ID:         id,
			Actual:     existing,
		}
		if desiredHash == actualHash {
			plan.Unchanged = append(plan.Unchanged, unchanged)
			continue
		}
		patch, changes, err := updateBody(d.Create, existing.Interface(), k.UpdateProto)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", k.Name, name, err)
		}
		if !changes {
			plan.Unchanged = append(plan.Unchanged, unchanged)
			continue
		}
		patchVal := reflect.ValueOf(patch)
		if patchVal.Kind() == reflect.Pointer {
			patchVal = patchVal.Elem()
		}
		plan.Updates = append(plan.Updates, Op{
			Name:       name,
			Source:     d.Source,
			Provenance: d.Provenance,
			ID:         id,
			Create:     createVal,
			Patch:      patchVal,
			Actual:     existing,
		})
	}

	for name, existing := range actualByName {
		if _, ok := desiredByName[name]; !ok {
			plan.Deletes = append(plan.Deletes, Op{
				Name:   name,
				ID:     k.IDFromResponse(existing.Interface()),
				Actual: existing,
			})
		}
	}
	sort.Slice(plan.Deletes, func(i, j int) bool { return plan.Deletes[i].Name < plan.Deletes[j].Name })

	return plan, nil
}

func updateBody(create, actual, updateProto any) (body any, changes bool, err error) {
	if IsPatchBody(updateProto) {
		return buildPatch(create, actual, updateProto)
	}
	return ToUpdate(create, updateProto), true, nil
}
