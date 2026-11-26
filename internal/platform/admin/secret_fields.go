// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"sync"
)

const redactedSentinel = "<REDACTED>"

type secretFieldInfo struct {
	Required           bool
	ConditionalDisplay map[string]any
}

type secretFieldRegistry struct {
	connectorFields map[string]map[string]secretFieldInfo
	gatewayFields   map[string]map[string]secretFieldInfo
	connectorTypes  map[string]bool
}

var getSecretRegistry = sync.OnceValue(buildSecretRegistry)
var getHiddenRegistry = sync.OnceValue(buildHiddenRegistry)

func buildSecretRegistry() *secretFieldRegistry {
	reg := &secretFieldRegistry{
		connectorFields: make(map[string]map[string]secretFieldInfo),
		gatewayFields:   make(map[string]map[string]secretFieldInfo),
		connectorTypes:  make(map[string]bool),
	}

	defs, err := LoadConnectorTypeDefinitions()
	if err == nil {
		for _, def := range defs {
			for _, st := range def.Subtypes {
				key := def.Type + "/" + st.Subtype
				reg.connectorTypes[key] = true
				fields := make(map[string]secretFieldInfo)
				for _, f := range st.FieldSchema {
					if f.Secret {
						fields[f.Name] = secretFieldInfo{
							Required:           f.Required,
							ConditionalDisplay: schemaConditionalDisplay(f.VisibleWhen),
						}
					}
				}
				if len(fields) > 0 {
					reg.connectorFields[key] = fields
				}
			}
		}
	}

	for _, schema := range getAllGatewaySchemas() {
		fields := make(map[string]secretFieldInfo)
		schemaMap, ok := schema.Schema.(map[string]any)
		if !ok {
			continue
		}
		for fieldName, fieldDef := range schemaMap {
			defMap, ok := fieldDef.(map[string]any)
			if !ok {
				continue
			}
			if secret, _ := defMap["secret"].(bool); secret {
				info := secretFieldInfo{ConditionalDisplay: schemaConditionalDisplay(defMap["conditionalDisplay"])}
				info.Required, _ = defMap["required"].(bool)
				fields[fieldName] = info
			}
		}
		if len(fields) > 0 {
			reg.gatewayFields[schema.Type] = fields
		}
	}

	return reg
}

func (r *secretFieldRegistry) ConnectorSecretFields(connType, subtype string) map[string]secretFieldInfo {
	return r.connectorFields[connType+"/"+subtype]
}

func (r *secretFieldRegistry) HasConnectorType(connType, subtype string) bool {
	return r.connectorTypes[connType+"/"+subtype]
}

func (r *secretFieldRegistry) GatewaySecretFields(gwType string) map[string]secretFieldInfo {
	return r.gatewayFields[gwType]
}

const RedactedSentinel = redactedSentinel

func ConnectorSecretFieldsKnown(connType, subtype string) bool {
	reg := getSecretRegistry()
	if reg == nil {
		return false
	}
	return reg.HasConnectorType(connType, subtype)
}

type hiddenFieldRegistry struct {
	connectorFields map[string]map[string]bool
}

func buildHiddenRegistry() *hiddenFieldRegistry {
	reg := &hiddenFieldRegistry{
		connectorFields: make(map[string]map[string]bool),
	}
	defs, err := LoadConnectorTypeDefinitions()
	if err == nil {
		for _, def := range defs {
			for _, st := range def.Subtypes {
				key := def.Type + "/" + st.Subtype
				fields := make(map[string]bool)
				for _, f := range st.FieldSchema {
					if f.Hidden {
						fields[f.Name] = true
					}
				}
				if len(fields) > 0 {
					reg.connectorFields[key] = fields
				}
			}
		}
	}
	return reg
}

func (r *hiddenFieldRegistry) ConnectorHiddenFields(connType, subtype string) map[string]bool {
	return r.connectorFields[connType+"/"+subtype]
}

func mergeHiddenFields(newValues, existingValues map[string]any, hiddenFields map[string]bool) {
	for field := range hiddenFields {
		newVal, inNew := newValues[field]
		if inNew && newVal != nil {
			continue
		}
		if oldVal, inOld := existingValues[field]; inOld {
			newValues[field] = oldVal
		} else {
			delete(newValues, field)
		}
	}
}

func redactSecretFields(values map[string]any, secretFields map[string]secretFieldInfo) map[string]any {
	if len(secretFields) == 0 || len(values) == 0 {
		return values
	}
	result := make(map[string]any, len(values))
	for k, v := range values {
		if _, isSecret := secretFields[k]; isSecret {
			if v != nil {
				result[k] = redactedSentinel
			}
		} else {
			result[k] = v
		}
	}
	return result
}

func mergeSecretFields(newValues, existingValues map[string]any, secretFields map[string]secretFieldInfo) (map[string]any, error) {
	if len(secretFields) == 0 || len(existingValues) == 0 {
		return newValues, nil
	}

	effectiveValues := make(map[string]any, len(existingValues)+len(newValues))
	for k, v := range existingValues {
		effectiveValues[k] = v
	}
	for k, v := range newValues {
		effectiveValues[k] = v
	}

	for field, info := range secretFields {
		oldVal, inOld := existingValues[field]

		if !conditionalDisplayMet(info.ConditionalDisplay, effectiveValues) {
			delete(newValues, field)
			continue
		}

		newVal, inNew := newValues[field]
		if !inNew {
			if inOld {
				newValues[field] = oldVal
			}
			continue
		}

		if newVal == nil {
			if inOld {
				newValues[field] = oldVal
			} else {
				delete(newValues, field)
			}
			continue
		}

		strVal, isStr := newVal.(string)
		if !isStr {
			return nil, &ValidationError{Message: fmt.Sprintf(
				"secret field %q must be a string, got %T", field, newVal)}
		}

		switch {
		case strVal == redactedSentinel:
			if inOld {
				newValues[field] = oldVal
			} else {
				delete(newValues, field)
			}
		case strVal == "" && info.Required:
			if inOld {
				newValues[field] = oldVal
			}
		case strVal == "":
			newValues[field] = nil
		}
	}
	return newValues, nil
}
