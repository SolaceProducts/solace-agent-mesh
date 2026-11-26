// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic

import (
	"fmt"
	"reflect"
)

type XRefRegistry interface {
	LookupID(kind, name string) (string, error)
	LookupName(kind, id string) (string, error)
}

func ResolveForward(dto any, reg XRefRegistry) error {
	return walkXRef(dto, reg, true)
}

func ResolveReverse(dto any, reg XRefRegistry) error {
	return walkXRef(dto, reg, false)
}

func walkXRef(dto any, reg XRefRegistry, forward bool) error {
	v := reflect.ValueOf(dto)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tags := parseCfgApplyTags(sf.Tag.Get("cfgapply"))
		kind, ok := tags.XRefKind()
		if !ok {
			continue
		}
		if err := rewriteXRef(v.Field(i), kind, reg, forward); err != nil {
			return fmt.Errorf("xref %s.%s: %w", t.Name(), sf.Name, err)
		}
	}
	return nil
}

func rewriteXRef(fv reflect.Value, kind string, reg XRefRegistry, forward bool) error {
	if !fv.CanSet() {
		return nil
	}
	switch fv.Kind() {
	case reflect.String:
		s := fv.String()
		if s == "" {
			return nil
		}
		out, err := lookupOne(reg, kind, s, forward)
		if err != nil {
			return err
		}
		fv.SetString(out)
	case reflect.Slice:
		if fv.Type().Elem().Kind() != reflect.String {
			return nil
		}
		for j := range fv.Len() {
			elem := fv.Index(j)
			s := elem.String()
			if s == "" {
				continue
			}
			out, err := lookupOne(reg, kind, s, forward)
			if err != nil {
				return err
			}
			elem.SetString(out)
		}
	case reflect.Pointer:
		if fv.IsNil() {
			return nil
		}
		return rewriteXRef(fv.Elem(), kind, reg, forward)
	}
	return nil
}

func lookupOne(reg XRefRegistry, kind, val string, forward bool) (string, error) {
	if forward {
		return reg.LookupID(kind, val)
	}
	return reg.LookupName(kind, val)
}
