// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

type Kind struct {
	Name   string
	Plural string

	Order int

	CreateProto   any
	UpdateProto   any
	ResponseProto any

	NameFromCreate   func(any) string
	NameFromResponse func(any) string

	IDFromResponse func(any) string

	ListFilter func(any) bool
}

func (k Kind) CreateType() reflect.Type {
	return reflect.TypeOf(k.CreateProto)
}

func (k Kind) UpdateType() reflect.Type {
	return reflect.TypeOf(k.UpdateProto)
}

func (k Kind) ResponseType() reflect.Type {
	return reflect.TypeOf(k.ResponseProto)
}

var (
	regMu sync.RWMutex
	reg   = map[string]Kind{}
)

func Register(k Kind) {
	if err := k.validateTags(); err != nil {
		panic(fmt.Sprintf("generic: register kind %q: %v", k.Name, err))
	}
	regMu.Lock()
	defer regMu.Unlock()
	reg[k.Name] = k
}

func (k Kind) validateTags() error {
	if err := validatePatchTags(k.UpdateProto); err != nil {
		return err
	}
	for _, proto := range []any{k.CreateProto, k.UpdateProto, k.ResponseProto} {
		t := reflect.TypeOf(proto)
		for t != nil && t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t == nil || t.Kind() != reflect.Struct {
			continue
		}
		for i := range t.NumField() {
			sf := t.Field(i)
			if !parseCfgApplyTags(sf.Tag.Get("cfgapply")).has("default") {
				continue
			}
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() != reflect.String {
				return fmt.Errorf("%s.%s: cfgapply default= applies only to string fields, not %s", t.Name(), sf.Name, sf.Type)
			}
		}
	}
	return nil
}

func Lookup(name string) (Kind, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	k, ok := reg[name]
	return k, ok
}

func OrderedKinds() []Kind {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Kind, 0, len(reg))
	for _, k := range reg {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func Each(fn func(Kind)) {
	for _, k := range OrderedKinds() {
		fn(k)
	}
}
