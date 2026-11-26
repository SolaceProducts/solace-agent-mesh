// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typeschema

import (
	"sort"
	"strings"
)

type TypeFieldSchema struct {
	Name        string
	Label       string
	Type        string
	Required    bool
	Secret      bool
	Default     any
	Hint        string
	Placeholder string
	OneOf       []TypeFieldOption
	Pattern     string
	MinLen      int
	MaxLen      int
	MinValue    *int
	MaxValue    *int
	Order       float64
	ShownWhen   map[string]any `json:"-"`
}

type TypeFieldOption struct {
	Value       string
	Label       string
	Description string
}

type GatewayTypeSchema struct {
	Type             string
	Name             string
	Description      string
	Warning          string
	YAMLTemplatePath string
	RequiredEnvVars  []string
	Fields           []TypeFieldSchema
}

type ConnectorSubtypeSchema struct {
	Type               string
	Subtype            string
	Name               string
	Description        string
	UsageGuidance      string
	Warning            string
	ConnectionTemplate string
	Experimental       bool
	Fields             []TypeFieldSchema
}

func SortedFields(fs []TypeFieldSchema) []TypeFieldSchema {
	out := make([]TypeFieldSchema, len(fs))
	copy(out, fs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func IsSecretType(t string) bool {
	switch strings.ToLower(t) {
	case "password", "secret":
		return true
	}
	return false
}
