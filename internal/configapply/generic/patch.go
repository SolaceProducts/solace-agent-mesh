// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
)

type PatchField interface {
	PatchState() (set, null bool)
}

var patchFieldType = reflect.TypeFor[PatchField]()

const clearableTag = "clearable"

const clearEmptyTag = "clear-empty"

type NotClearableError struct {
	Field string
}

func (e *NotClearableError) Error() string {
	return fmt.Sprintf("%s was removed, but the server cannot clear it; set a value instead", e.Field)
}

func IsPatchBody(updateProto any) bool {
	t := derefType(reflect.TypeOf(updateProto))
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	for sf := range t.Fields() {
		if sf.Type.Implements(patchFieldType) {
			return true
		}
	}
	return false
}

func ToPatch(create, actual, updateProto any) (any, error) {
	body, _, err := buildPatch(create, actual, updateProto)
	return body, err
}

func buildPatch(create, actual, updateProto any) (any, bool, error) {
	dstType := derefType(reflect.TypeOf(updateProto))
	if dstType == nil || dstType.Kind() != reflect.Struct {
		return nil, false, fmt.Errorf("update prototype %T is not a struct", updateProto)
	}
	desired := wireView(create)
	current := wireView(actual)

	body := map[string]any{}
	changes := false
	for sf := range dstType.Fields() {
		name := jsonTagName(sf)
		tags := parseCfgApplyTags(sf.Tag.Get("cfgapply"))
		if !sf.IsExported() || name == "" || name == "-" || tags.has("read-only") {
			continue
		}
		want, described := desired[name]
		if !described || (want.leftOut && !canClear(sf, tags)) {
			continue
		}
		have := current[name]
		value, send, err := patchFieldValue(name, sf, tags, want.value, have.value)
		if err != nil {
			return nil, false, err
		}
		if !send {
			continue
		}
		body[name] = value
		if _, changesBeyondSecrets, _ := patchFieldValue(name, sf, tags, withoutRedacted(want.value, have.redacted), have.value); changesBeyondSecrets {
			changes = true
		}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, false, fmt.Errorf("encode %s: %w", dstType.Name(), err)
	}
	out := reflect.New(dstType)
	if err := json.Unmarshal(raw, out.Interface()); err != nil {
		return nil, false, fmt.Errorf("build %s: %w", dstType.Name(), err)
	}
	if reflect.TypeOf(updateProto).Kind() == reflect.Pointer {
		return out.Interface(), changes, nil
	}
	return out.Elem().Interface(), changes, nil
}

func canClear(sf reflect.StructField, tags cfgapplyTags) bool {
	_, merges := mergeDepth(tags)
	return sf.Tag.Get(clearableTag) == "true" || tags.has(clearEmptyTag) || merges
}

func patchFieldValue(name string, sf reflect.StructField, tags cfgapplyTags, want, have any) (value any, send bool, err error) {
	switch {
	case isAbsent(want) && isAbsent(have):
		return nil, false, nil
	case isAbsent(want):
		return clearedValue(name, sf, tags, have)
	}
	if depth, merges := mergeDepth(tags); merges {
		wantMap, wantIsMap := want.(map[string]any)
		haveMap, haveIsMap := have.(map[string]any)
		if wantIsMap && (haveIsMap || isAbsent(have)) {
			return mergedValue(name, tags, wantMap, haveMap, depth)
		}
	}
	if reflect.DeepEqual(want, have) {
		return nil, false, nil
	}
	return want, true, nil
}

func clearedValue(name string, sf reflect.StructField, tags cfgapplyTags, have any) (any, bool, error) {
	switch {
	case sf.Tag.Get(clearableTag) == "true":
		return nil, true, nil
	case tags.has(clearEmptyTag):
		if value, ok := patchValueType(sf.Type); ok && value.Kind() == reflect.Map {
			return map[string]any{}, true, nil
		}
		return "", true, nil
	}
	if depth, merges := mergeDepth(tags); merges {
		if haveMap, ok := have.(map[string]any); ok {
			return mergedValue(name, tags, nil, haveMap, depth)
		}
	}
	return nil, false, &NotClearableError{Field: name}
}

func mergedValue(name string, tags cfgapplyTags, want, have map[string]any, depth int) (any, bool, error) {
	changed := mergeChanges(want, have, depth)
	switchMember := tags.values["switch"]
	if switchMember == "" {
		return changed, len(changed) > 0, nil
	}
	if sent, ok := changed[switchMember]; ok && sent == nil {
		return nil, false, &NotClearableError{Field: name + "." + switchMember}
	}
	if _, switched := changed[switchMember]; switched {
		for k, w := range want {
			changed[k] = w
		}
	}
	return changed, len(changed) > 0, nil
}

func mergeChanges(want, have map[string]any, depth int) map[string]any {
	out := map[string]any{}
	for k, w := range want {
		h := have[k]
		if reflect.DeepEqual(w, h) {
			continue
		}
		wm, wantIsMap := w.(map[string]any)
		hm, haveIsMap := h.(map[string]any)
		if depth > 1 && wantIsMap && (haveIsMap || h == nil) {
			if sub := mergeChanges(wm, hm, depth-1); len(sub) > 0 {
				out[k] = sub
			}
			continue
		}
		out[k] = w
	}
	for k, h := range have {
		if _, kept := want[k]; kept {
			continue
		}
		if hm, isMap := h.(map[string]any); depth > 1 && isMap {
			if sub := mergeChanges(nil, hm, depth-1); len(sub) > 0 {
				out[k] = sub
			}
			continue
		}
		out[k] = nil
	}
	return out
}

func mergeDepth(tags cfgapplyTags) (int, bool) {
	raw, ok := tags.values["merge"]
	if !ok {
		return 0, false
	}
	if raw == "" {
		return 1, true
	}
	depth, _ := strconv.Atoi(raw)
	return max(depth, 1), true
}

type wireField struct {
	value    any
	redacted any
	leftOut  bool
}

func wireView(dto any) map[string]wireField {
	out := map[string]wireField{}
	v := reflect.ValueOf(dto)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return out
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return out
	}
	for i := range v.NumField() {
		sf := v.Type().Field(i)
		name := jsonTagName(sf)
		tags := parseCfgApplyTags(sf.Tag.Get("cfgapply"))
		if !sf.IsExported() || name == "" || name == "-" || tags.has("read-only") {
			continue
		}
		fv := v.Field(i)
		field := wireField{leftOut: fv.Kind() == reflect.Pointer && fv.IsNil()}
		val := jsonValue(fv, tags)
		if tags.has("secret") {
			field.redacted = redactedMembers(val)
			val = withoutRedacted(val, field.redacted)
		}
		if def, ok := tags.values["default"]; ok && (val == nil || val == "") {
			val = def
		}
		field.value = pruneNulls(val)
		out[name] = field
	}
	return out
}

func jsonValue(fv reflect.Value, tags cfgapplyTags) any {
	if tags.has("json-object") {
		return projectJSONObject(fv)
	}
	raw, err := json.Marshal(fv.Interface())
	if err != nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var val any
	if err := dec.Decode(&val); err != nil {
		return nil
	}
	return val
}

func redactedMembers(v any) any {
	switch t := v.(type) {
	case string:
		if t == RedactedSentinel {
			return true
		}
	case map[string]any:
		marks := map[string]any{}
		for k, m := range t {
			if mark := redactedMembers(m); mark != nil {
				marks[k] = mark
			}
		}
		if len(marks) > 0 {
			return marks
		}
	}
	return nil
}

func withoutRedacted(v, redacted any) any {
	if redacted == true {
		return nil
	}
	marks, ok := redacted.(map[string]any)
	m, isMap := v.(map[string]any)
	if !ok || !isMap {
		return v
	}
	out := make(map[string]any, len(m))
	for k, member := range m {
		if mark, marked := marks[k]; marked {
			member = withoutRedacted(member, mark)
			if member == nil {
				continue
			}
		}
		out[k] = member
	}
	return out
}

func pruneNulls(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, m := range t {
			if m == nil {
				continue
			}
			out[k] = pruneNulls(m)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, m := range t {
			out[i] = pruneNulls(m)
		}
		return out
	default:
		return v
	}
}

func isAbsent(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	default:
		return false
	}
}

func derefType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func validatePatchTags(updateProto any) error {
	t := derefType(reflect.TypeOf(updateProto))
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	for sf := range t.Fields() {
		tags := parseCfgApplyTags(sf.Tag.Get("cfgapply"))
		raw, merges := tags.values["merge"]
		clearsEmpty := tags.has(clearEmptyTag)
		switches := tags.has("switch")
		if !merges && !clearsEmpty && !switches {
			continue
		}
		value, ok := patchValueType(sf.Type)
		if !ok {
			return fmt.Errorf("%s.%s: cfgapply merge, clear-empty and switch apply only to PATCH fields, not %s", t.Name(), sf.Name, sf.Type)
		}
		if merges && value.Kind() != reflect.Map {
			return fmt.Errorf("%s.%s: cfgapply merge applies only to objects, not %s", t.Name(), sf.Name, value)
		}
		if depth, err := strconv.Atoi(raw); merges && raw != "" && (err != nil || depth < 1) {
			return fmt.Errorf("%s.%s: cfgapply merge=%s is not a depth of 1 or more", t.Name(), sf.Name, raw)
		}
		if clearsEmpty && value.Kind() != reflect.String && value.Kind() != reflect.Map {
			return fmt.Errorf("%s.%s: cfgapply clear-empty applies only to strings and objects, not %s", t.Name(), sf.Name, value)
		}
		if switches && (!merges || tags.values["switch"] == "") {
			return fmt.Errorf("%s.%s: cfgapply switch names a member of a merged object, so it needs merge and a member name", t.Name(), sf.Name)
		}
	}
	return nil
}

func patchValueType(t reflect.Type) (reflect.Type, bool) {
	if !t.Implements(patchFieldType) || t.Kind() != reflect.Struct {
		return nil, false
	}
	value, ok := t.FieldByName("Value")
	if !ok {
		return nil, false
	}
	return value.Type, true
}
