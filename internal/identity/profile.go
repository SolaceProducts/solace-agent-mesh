// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"encoding/json"
	"fmt"
)

type Profile struct {
	ID            string
	DisplayName   string
	PreferredName string
	WorkEmail     string
	JobTitle      string
	Department    string
	Location      string

	Extra map[string]any
}

func (p Profile) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(p.Extra)+7)
	for k, v := range p.Extra {
		m[k] = v
	}
	setIf := func(key, val string) {
		if val != "" {
			m[key] = val
		}
	}
	setIf("displayName", p.DisplayName)
	setIf("preferredName", p.PreferredName)
	setIf("workEmail", p.WorkEmail)
	setIf("jobTitle", p.JobTitle)
	setIf("department", p.Department)
	setIf("location", p.Location)
	m["id"] = p.ID
	return json.Marshal(m)
}

func (p *Profile) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var all map[string]any
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	id, ok := all["id"].(string)
	if !ok || id == "" {
		return fmt.Errorf("identity profile: required field \"id\" is missing, empty, or not a string")
	}
	delete(all, "id")
	out := Profile{ID: id}
	namedString(all, "displayName", &out.DisplayName)
	namedString(all, "preferredName", &out.PreferredName)
	namedString(all, "workEmail", &out.WorkEmail)
	namedString(all, "jobTitle", &out.JobTitle)
	namedString(all, "department", &out.Department)
	namedString(all, "location", &out.Location)
	if len(all) > 0 {
		out.Extra = all
	}
	*p = out
	return nil
}

func namedString(all map[string]any, key string, dst *string) {
	if v, ok := all[key].(string); ok {
		*dst = v
		delete(all, key)
	}
}

type TimeOffEntry struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	Type   string `json:"type"`
	Amount string `json:"amount"`
}
