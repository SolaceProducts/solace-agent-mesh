// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
)

const RedactedSentinel = "<REDACTED>"

func Hash(dto any, redactedKeys map[string]struct{}) (string, error) {
	view := projectForHash(dto, redactedKeys)
	return canonicalJSONHash(view)
}

func ProjectForHash(dto any, redactedKeys map[string]struct{}) map[string]any {
	return projectForHash(dto, redactedKeys)
}

func projectForHash(dto any, redactedKeys map[string]struct{}) map[string]any {
	out := map[string]any{}
	v := reflect.ValueOf(dto)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return out
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return out
	}
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tags := parseCfgApplyTags(sf.Tag.Get("cfgapply"))
		if tags.has("omit-hash") || tags.has("read-only") {
			continue
		}
		jsonName := jsonTagName(sf)
		if jsonName == "" || jsonName == "-" {
			continue
		}
		fv := v.Field(i)
		val := projectFieldValue(fv)
		if def, ok := tags.values["default"]; ok && val == "" {
			val = def
		}
		if tags.has("secret") {
			val = applySecretRedaction(val, redactedKeys)
		}
		if tags.has("json-object") {
			val = projectJSONObject(fv)
		}
		out[jsonName] = val
	}
	return out
}

func projectJSONObject(fv reflect.Value) any {
	if fv.Kind() != reflect.Slice || fv.IsNil() || fv.Len() == 0 {
		return nil
	}
	raw, ok := fv.Interface().(json.RawMessage)
	if !ok {
		return fv.Interface()
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	return parsed
}

func projectFieldValue(fv reflect.Value) any {
	switch fv.Kind() {
	case reflect.Pointer:
		if fv.IsNil() {
			return zeroOf(fv.Type().Elem())
		}
		return projectFieldValue(fv.Elem())
	case reflect.Map:
		if fv.IsNil() {
			return map[string]any{}
		}
		out := make(map[string]any, fv.Len())
		iter := fv.MapRange()
		for iter.Next() {
			k, ok := iter.Key().Interface().(string)
			if !ok {
				continue
			}
			out[k] = withEmptyContainers(iter.Value()).Interface()
		}
		return out
	case reflect.Slice:
		if fv.IsNil() {
			return []any{}
		}
		return withEmptyContainers(fv).Interface()
	default:
		return withEmptyContainers(fv).Interface()
	}
}

func withEmptyContainers(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(withEmptyContainers(v.Index(i)))
		}
		return out
	case reflect.Map:
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), withEmptyContainers(iter.Value()))
		}
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(withEmptyContainers(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if out.Field(i).CanSet() {
				out.Field(i).Set(withEmptyContainers(v.Field(i)))
			}
		}
		return out
	default:
		return v
	}
}

func zeroOf(t reflect.Type) any {
	return reflect.Zero(t).Interface()
}

func applySecretRedaction(val any, redactedKeys map[string]struct{}) any {
	m, ok := val.(map[string]any)
	if !ok {
		return val
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok && s == RedactedSentinel {
			continue
		}
		if _, drop := redactedKeys[k]; drop {
			continue
		}
		out[k] = v
	}
	return out
}

func RedactedKeysFromSecretField(dto any) map[string]struct{} {
	out := map[string]struct{}{}
	v := reflect.ValueOf(dto)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return out
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return out
	}
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		tags := parseCfgApplyTags(sf.Tag.Get("cfgapply"))
		if !tags.has("secret") {
			continue
		}
		fv := v.Field(i)
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue
			}
			fv = fv.Elem()
		}
		fv, ok := unwrapAnySecretFieldToReachRedactedKeys(fv)
		if !ok {
			continue
		}
		if fv.Kind() != reflect.Map {
			continue
		}
		iter := fv.MapRange()
		for iter.Next() {
			s, ok := iter.Value().Interface().(string)
			if !ok || s != RedactedSentinel {
				continue
			}
			k, ok := iter.Key().Interface().(string)
			if !ok {
				continue
			}
			out[k] = struct{}{}
		}
	}
	return out
}

func unwrapAnySecretFieldToReachRedactedKeys(fv reflect.Value) (reflect.Value, bool) {
	if fv.Kind() != reflect.Interface {
		return fv, true
	}
	if fv.IsNil() {
		return fv, false
	}
	return fv.Elem(), true
}

func canonicalJSONHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type cfgapplyTags struct {
	values map[string]string
}

func (c cfgapplyTags) has(key string) bool {
	if c.values == nil {
		return false
	}
	_, ok := c.values[key]
	return ok
}

func (c cfgapplyTags) XRefKind() (string, bool) {
	if c.values == nil {
		return "", false
	}
	v, ok := c.values["xref"]
	return v, ok
}

func parseCfgApplyTags(raw string) cfgapplyTags {
	tags := cfgapplyTags{values: map[string]string{}}
	if raw == "" {
		return tags
	}
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if k, v, ok := strings.Cut(part, "="); ok {
			tags.values[k] = v
		} else {
			tags.values[part] = ""
		}
	}
	return tags
}

func jsonTagName(sf reflect.StructField) string {
	tag := sf.Tag.Get("json")
	if tag == "" {
		return sf.Name
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	return tag
}
