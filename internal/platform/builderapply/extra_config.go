// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

var dtoOwnedCardKeys = []string{"description", "skills", "defaultInputModes", "defaultOutputModes"}

func (r *agentFieldReader) readExtraConfig(appConfig, spec map[string]any) {
	byRuntime := runtimeIndex(extraConfigKeys)
	stated := map[string]any{}
	for _, key := range slices.Sorted(maps.Keys(appConfig)) {
		canonical, registered := byRuntime[key]
		_, read := r.consumed[key]
		if read || !registered || key == agentModelKey || slices.Contains(agentKeysManagedElsewhere, key) {
			continue
		}
		r.consumed[key] = struct{}{}
		if value, ok := r.recognisedExtra(appConfig[key], extraConfigKeys[canonical], key); ok {
			stated[key] = value
		}
	}
	if card, isCard := appConfig["agent_card"].(map[string]any); isCard {
		extras := maps.Clone(card)
		for _, owned := range dtoOwnedCardKeys {
			delete(extras, owned)
		}
		if block := r.recognisedBlock(extras, extraConfigKeys["agentCard"].Fields, "agent_card."); len(block) > 0 {
			stated["agent_card"] = block
		}
	}
	if len(stated) == 0 {
		return
	}
	additional := mapField(spec, "additionalConfigurations")
	if additional == nil {
		additional = map[string]any{}
	}
	maps.Copy(additional, entplatform.ReverseTranslateAgentExtraConfig(stated))
	spec["additionalConfigurations"] = additional
}

func (r *agentFieldReader) recognisedExtra(value any, entry entplatform.AgentExtraConfigKey, label string) (any, bool) {
	if _, err := json.Marshal(value); err != nil {
		if _, nonFinite := errors.AsType[*json.UnsupportedValueError](err); nonFinite {
			r.refusals = append(r.refusals, fmt.Sprintf("%s holds a number that is not finite (.nan or .inf); use a finite number", label))
			return nil, false
		}
		r.warnings = append(r.warnings, droppedAgentKeyWarning(label, "holds a value with non-text keys and was not saved"))
		return nil, false
	}
	switch block, isBlock := value.(map[string]any); {
	case entry.Fields != nil && isBlock:
		return r.recognisedBlock(block, entry.Fields, label+"."), true
	case entry.Items != nil:
		held, isList := value.([]any)
		if !isList {
			return value, true
		}
		items := slices.Clone(held)
		for i, item := range items {
			if m, isMap := item.(map[string]any); isMap {
				items[i] = r.recognisedBlock(m, entry.Items, fmt.Sprintf("%s[%d].", label, i))
			}
		}
		return items, true
	}
	return value, true
}

func (r *agentFieldReader) recognisedBlock(block map[string]any, fields map[string]entplatform.AgentExtraConfigKey, prefix string) map[string]any {
	byRuntime := runtimeIndex(fields)
	out := make(map[string]any, len(block))
	for _, key := range slices.Sorted(maps.Keys(block)) {
		canonical, known := byRuntime[key]
		if !known {
			r.warnings = append(r.warnings, droppedAgentKeyWarning(prefix+key, "was not recognised and was not saved"))
			continue
		}
		if v, ok := r.recognisedExtra(block[key], fields[canonical], prefix+key); ok {
			out[key] = v
		}
	}
	return out
}

func runtimeIndex(keys map[string]entplatform.AgentExtraConfigKey) map[string]string {
	out := make(map[string]string, len(keys))
	for canonical, k := range keys {
		out[k.Runtime] = canonical
	}
	return out
}

func extraConfigOf(spec map[string]any) map[string]any {
	stated := maps.Clone(mapField(spec, "additionalConfigurations"))
	delete(stated, interAgentCommunicationStoredKey)
	if len(stated) == 0 {
		return nil
	}
	return stated
}

func withoutRemovals(stated map[string]any, keys map[string]entplatform.AgentExtraConfigKey) map[string]any {
	out := make(map[string]any, len(stated))
	for canonical, value := range stated {
		if value == nil {
			continue
		}
		if block, isBlock := value.(map[string]any); isBlock && keys[canonical].Fields != nil {
			pruned := withoutRemovals(block, keys[canonical].Fields)
			if len(pruned) == 0 {
				continue
			}
			value = pruned
		}
		out[canonical] = value
	}
	return out
}

func checkWrittenExtraConfig(componentName string, written map[string]any) ([]string, error) {
	raw, err := json.Marshal(written)
	if err != nil {
		return nil, fmt.Errorf("agent %q: encode additional settings: %w", componentName, err)
	}
	warnings, err := entplatform.ValidateAgentConfigurationsShape(raw)
	return warnings, namedValidationError(componentName, err)
}

func checkExtraConfigBounds(written, stored json.RawMessage) ([]string, error) {
	if stored == nil {
		return entplatform.ValidateAgentConfigurations(written)
	}
	return entplatform.ValidateAgentConfigurationsUpdate(written, func() (json.RawMessage, error) { return stored, nil })
}

func storedInCanonicalSpelling(stored json.RawMessage) json.RawMessage {
	var blob map[string]any
	if json.Unmarshal(stored, &blob) != nil {
		return json.RawMessage("{}")
	}
	canonical, err := json.Marshal(entplatform.ReverseTranslateAgentExtraConfig(blob))
	if err != nil {
		return json.RawMessage("{}")
	}
	return canonical
}

func namedValidationError(componentName string, err error) error {
	ve, isValidation := errors.AsType[*entplatform.ValidationError](err)
	if !isValidation {
		return err
	}
	message := ve.Message
	if componentName != "" {
		message = fmt.Sprintf("agent %q: %s", componentName, ve.Message)
	}
	if problems := ve.Details[ve.Field]; len(problems) > 0 {
		message += ": " + strings.Join(problems, "; ")
	}
	return &entplatform.ValidationError{Message: message, Field: ve.Field, Details: ve.Details}
}
