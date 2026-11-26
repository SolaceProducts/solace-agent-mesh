// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic

import (
	"fmt"
	"reflect"
)

func ToUpdate(create, updateProto any) any {
	if IsPatchBody(updateProto) {
		panic(fmt.Sprintf("generic.ToUpdate: %T is a PATCH body; build it with ToPatch", updateProto))
	}
	src := reflect.ValueOf(create)
	for src.Kind() == reflect.Pointer {
		if src.IsNil() {
			return nil
		}
		src = src.Elem()
	}
	if src.Kind() != reflect.Struct {
		return nil
	}

	dstType := reflect.TypeOf(updateProto)
	for dstType.Kind() == reflect.Pointer {
		dstType = dstType.Elem()
	}
	if dstType.Kind() != reflect.Struct {
		return nil
	}
	dst := reflect.New(dstType).Elem()

	srcByJSON := map[string]reflect.Value{}
	for i := range src.NumField() {
		jn := jsonTagName(src.Type().Field(i))
		if jn == "" || jn == "-" {
			continue
		}
		srcByJSON[jn] = src.Field(i)
	}

	for i := range dst.NumField() {
		dsf := dst.Type().Field(i)
		if !dsf.IsExported() {
			continue
		}
		dtags := parseCfgApplyTags(dsf.Tag.Get("cfgapply"))
		if dtags.has("read-only") {
			continue
		}
		jn := jsonTagName(dsf)
		if jn == "" || jn == "-" {
			continue
		}
		sv, ok := srcByJSON[jn]
		if !ok {
			continue
		}
		assignField(dst.Field(i), sv)
	}

	out := reflect.New(dstType)
	out.Elem().Set(dst)
	if reflect.TypeOf(updateProto).Kind() == reflect.Pointer {
		return out.Interface()
	}
	return out.Elem().Interface()
}

func assignField(dst, sv reflect.Value) {
	if !dst.CanSet() {
		return
	}
	dt := dst.Type()
	st := sv.Type()
	if st.AssignableTo(dt) {
		dst.Set(sv)
		return
	}
	if dt.Kind() == reflect.Pointer && st.AssignableTo(dt.Elem()) {
		ptr := reflect.New(dt.Elem())
		ptr.Elem().Set(sv)
		dst.Set(ptr)
		return
	}
	if dt.Kind() == reflect.Pointer && dt.Elem().Kind() == reflect.Slice && st.Kind() == reflect.Slice && st.Elem() == dt.Elem().Elem() {
		ptr := reflect.New(dt.Elem())
		ptr.Elem().Set(sv)
		dst.Set(ptr)
		return
	}
	if st.Kind() == reflect.Pointer && st.Elem().AssignableTo(dt) {
		if !sv.IsNil() {
			dst.Set(sv.Elem())
		}
	}
}
