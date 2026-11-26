// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"bytes"
	"encoding/json"
	"reflect"
)

func splitOpenObject(data []byte, declared map[string]any) (extensions map[string]json.RawMessage, present map[string]bool, err error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	extensions = map[string]json.RawMessage{}
	present = map[string]bool{}
	for key, value := range raw {
		target, isDeclared := declared[key]
		if !isDeclared {
			extensions[key] = value
			continue
		}
		if !decodeDeclared(value, target) {
			extensions[key] = value
			continue
		}
		present[key] = true
	}
	return extensions, present, nil
}

func joinOpenObject(declared map[string]any, extensions map[string]json.RawMessage) ([]byte, error) {
	out := make(map[string]any, len(declared)+len(extensions))
	for key, value := range extensions {
		out[key] = value
	}
	for key, value := range declared {
		out[key] = value
	}
	return json.Marshal(out)
}

func decodeDeclared(value json.RawMessage, target any) bool {
	scratch := reflect.New(reflect.TypeOf(target).Elem())
	if err := json.NewDecoder(bytes.NewReader(value)).Decode(scratch.Interface()); err != nil {
		return false
	}
	reflect.ValueOf(target).Elem().Set(scratch.Elem())
	return true
}
