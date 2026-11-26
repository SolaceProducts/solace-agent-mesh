// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package connector

import "fmt"

var connectorEntryKeys = map[string]struct{}{
	"name":    {},
	"type":    {},
	"subtype": {},
	"config":  {},
}

func ParseEntry(entry map[string]any) (Config, error) {
	for k := range entry {
		if _, ok := connectorEntryKeys[k]; !ok {
			return Config{}, fmt.Errorf("unknown field %q (valid: name, type, subtype, config)", k)
		}
	}

	name, _ := entry["name"].(string)
	if name == "" {
		return Config{}, fmt.Errorf("missing required field `name`")
	}
	connType, _ := entry["type"].(string)
	if connType == "" {
		return Config{}, fmt.Errorf("connector %q: missing required field `type`", name)
	}
	subtype, _ := entry["subtype"].(string)

	var inner map[string]any
	if c, ok := entry["config"].(map[string]any); ok {
		inner = c
	}

	return Config{
		Name:    name,
		Type:    connType,
		Subtype: subtype,
		Config:  inner,
	}, nil
}
