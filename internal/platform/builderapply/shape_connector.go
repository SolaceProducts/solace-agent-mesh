// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const defaultDetailField = "values"

type connectorArtifact struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	Values      any    `json:"values"`

	Icon string `json:"icon"`
}

func desiredConnector(componentName, content string) (desiredComponent, error) {
	var art connectorArtifact
	if err := decodeJSONStrict([]byte(content), &art); err != nil {
		return desiredComponent{}, fmt.Errorf("parse connector %q: %w", componentName, err)
	}

	name := cmp.Or(art.Name, componentName)
	if name == "" {
		return desiredComponent{}, fmt.Errorf("connector has no name")
	}
	if art.Type == "" {
		return desiredComponent{}, fmt.Errorf("connector %q has no type", componentName)
	}

	create := entplatform.CreateConnectorRequest{
		Name:        name,
		Description: art.Description,
		Type:        art.Type,
		Subtype:     art.Subtype,
		Values:      art.Values,
	}
	authored, err := authoredJSONKeys(content, "name")
	if err != nil {
		return desiredComponent{}, fmt.Errorf("parse connector %q: %w", componentName, err)
	}

	authoredValues, _ := art.Values.(map[string]any)
	return desiredComponent{
		planProto:               create,
		create:                  create,
		authored:                authored,
		authoredConnectorValues: maps.Clone(authoredValues),
	}, nil
}

func rejectUndeclaredConnectorKeys(d desiredComponent) error {
	create, ok := d.create.(entplatform.CreateConnectorRequest)
	if !ok {
		return nil
	}
	return entplatform.ValidateConnectorValueKeys(create.Type, create.Subtype, valuesForKeyCheck(create, d.authoredConnectorValues))
}

func valuesForKeyCheck(create entplatform.CreateConnectorRequest, authored map[string]any) map[string]any {
	merged, _ := create.Values.(map[string]any)
	operations, stored := merged["operations"]
	if _, authoredOperations := authored["operations"]; !stored || authoredOperations || authored == nil {
		return authored
	}
	withOperations := maps.Clone(authored)
	withOperations["operations"] = operations
	return withOperations
}

func mergeValidationErrors(errs ...error) error {
	errs = slices.DeleteFunc(slices.Clone(errs), func(err error) bool { return err == nil })
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	}
	var merged *entplatform.ValidationError
	for _, err := range errs {
		ve, ok := errors.AsType[*entplatform.ValidationError](err)
		if !ok {
			return err
		}
		if merged == nil {
			merged = &entplatform.ValidationError{Message: ve.Message, Field: ve.Field, Details: cloneDetails(ve.Details)}
			continue
		}
		if len(merged.Details) == 0 {
			merged.Details = map[string][]string{cmp.Or(merged.Field, defaultDetailField): {merged.Message}}
		}
		merged.Message += "; " + ve.Message
		merged.Field = ""
		if len(ve.Details) == 0 {
			field := cmp.Or(ve.Field, defaultDetailField)
			merged.Details[field] = append(merged.Details[field], ve.Message)
			continue
		}
		for field, messages := range ve.Details {
			merged.Details[field] = append(merged.Details[field], messages...)
		}
	}
	return merged
}

func cloneDetails(details map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(details))
	for field, messages := range details {
		cloned[field] = slices.Clone(messages)
	}
	return cloned
}

func missingRequiredConnectorFieldWarnings(d desiredComponent, hasStored bool) []string {
	create, ok := d.create.(entplatform.CreateConnectorRequest)
	if !ok {
		return nil
	}
	missing := entplatform.MissingRequiredConnectorFields(create.Type, create.Subtype, create.Values, hasStored)
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"values has no %s, which the %s/%s connector requires; it will not work until that is set — write the real value, or <<__SAM_REQUIRED__>> to finish it later on the Connectors page",
		strings.Join(missing, ", "), create.Type, create.Subtype)}
}
