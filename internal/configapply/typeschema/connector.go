// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typeschema

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

var connectorCache = sync.OnceValue(buildConnectorTypeSchemas)

func ConnectorTypeSchemas() map[string]map[string]ConnectorSubtypeSchema {
	return connectorCache()
}

func ConnectorTypeNames() []string {
	m := ConnectorTypeSchemas()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ConnectorSubtypeNames(t string) []string {
	subs := ConnectorTypeSchemas()[t]
	out := make([]string, 0, len(subs))
	for k := range subs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func LookupConnectorType(t, sub string) (ConnectorSubtypeSchema, error) {
	subs, ok := ConnectorTypeSchemas()[t]
	if !ok {
		return ConnectorSubtypeSchema{}, fmt.Errorf("unknown connector type %q (known: %s)", t, strings.Join(ConnectorTypeNames(), ", "))
	}
	if sub == "" {
		names := ConnectorSubtypeNames(t)
		if len(names) == 0 {
			return ConnectorSubtypeSchema{}, fmt.Errorf("connector type %q has no subtypes", t)
		}
		sub = names[0]
	}
	s, ok := subs[sub]
	if !ok {
		return ConnectorSubtypeSchema{}, fmt.Errorf("unknown connector subtype %q for type %q (known: %s)",
			sub, t, strings.Join(ConnectorSubtypeNames(t), ", "))
	}
	return s, nil
}

func buildConnectorTypeSchemas() map[string]map[string]ConnectorSubtypeSchema {
	raws := entplatform.VisibleConnectorSchemas(featureRegistry())
	out := make(map[string]map[string]ConnectorSubtypeSchema, len(raws))
	for _, t := range raws {
		bySubtype := make(map[string]ConnectorSubtypeSchema, len(t.Subtypes))
		for _, sub := range t.Subtypes {
			schema := ConnectorSubtypeSchema{
				Type:               t.Type,
				Subtype:            sub.Subtype,
				Name:               sub.Name,
				Description:        sub.Description,
				UsageGuidance:      sub.UsageGuidance,
				Warning:            sub.Warning,
				ConnectionTemplate: sub.ConnectionTemplate,
				Experimental:       sub.Experimental,
				Fields:             SortedFields(projectConnectorFields(sub.FieldSchema)),
			}
			bySubtype[sub.Subtype] = schema
		}
		out[t.Type] = bySubtype
	}
	return out
}

func conditionalDisplayAsAny(in map[string]string) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func projectConnectorFields(in map[string]entplatform.ConnectorSchemaField) []TypeFieldSchema {
	out := make([]TypeFieldSchema, 0, len(in))
	for name, f := range in {
		entry := TypeFieldSchema{
			Name:        name,
			Label:       f.Label,
			Type:        f.Type,
			Required:    f.Required,
			Hint:        f.Hint,
			Placeholder: f.Placeholder,
			Default:     f.Default,
			Order:       float64(f.Order),
			MinLen:      -1,
			MaxLen:      -1,
			ShownWhen:   conditionalDisplayAsAny(f.ConditionalDisplay),
		}
		if IsSecretType(f.Type) || f.Secret {
			entry.Secret = true
		}
		if f.Validation != nil {
			if f.Validation.MinLength != nil {
				entry.MinLen = *f.Validation.MinLength
			}
			if f.Validation.MaxLength != nil {
				entry.MaxLen = *f.Validation.MaxLength
			}
			if f.Validation.Min != nil {
				v := *f.Validation.Min
				entry.MinValue = &v
			}
			if f.Validation.Max != nil {
				v := *f.Validation.Max
				entry.MaxValue = &v
			}
			if f.Validation.Pattern != "" {
				entry.Pattern = f.Validation.Pattern
			}
		}
		for _, opt := range f.Options {
			entry.OneOf = append(entry.OneOf, TypeFieldOption{
				Value:       opt.Value,
				Label:       opt.Label,
				Description: opt.Description,
			})
		}
		out = append(out, entry)
	}
	return out
}
