// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"reflect"
	"strings"
)

type extraConfigRuleViolation struct {
	topKey  string
	message string
}

func splitByStoredValue(violations []extraConfigRuleViolation, written map[string]any, stored json.RawMessage) (blocking, carried []string) {
	var storedObj map[string]any
	if len(stored) > 0 {
		_ = json.Unmarshal(stored, &storedObj)
	}
	for _, v := range violations {
		storedVal, wasStored := storedObj[v.topKey]
		if wasStored && reflect.DeepEqual(written[v.topKey], storedVal) {
			carried = append(carried, v.message)
			continue
		}
		blocking = append(blocking, v.message)
	}
	return blocking, carried
}

func topLevelKey(path string) string {
	key, _, _ := strings.Cut(path, ".")
	return key
}
