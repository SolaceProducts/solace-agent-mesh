// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh/solaceheaders"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

const (
	legacyTaskResponsePrefix = "task_response:"
	inputPayloadPrefix       = "input.payload:"
)

func validateEventMeshOutputDelivery(path string, output map[string]any) *ValidationError {
	mode, _ := output["deliveryMode"].(string)
	if err := solaceheaders.ValidateDeliveryMode(mode); err != nil {
		return &ValidationError{Message: fmt.Sprintf("%s.deliveryMode: %v", path, err)}
	}
	guaranteed := mode == solaceheaders.DeliveryGuaranteed

	headers, _ := output["headers"].(map[string]any)
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		if err := validateAuthoredHeader(name, headers[name], guaranteed); err != nil {
			return &ValidationError{Message: fmt.Sprintf("%s.headers.%s: %v", path, name, err)}
		}
	}
	props, _ := output["userProperties"].(map[string]any)
	for _, name := range slices.Sorted(maps.Keys(props)) {
		if err := validateAuthoredUserProperty(name, props[name]); err != nil {
			return &ValidationError{Message: fmt.Sprintf("%s.userProperties: %v", path, err)}
		}
	}
	return nil
}

func validateAuthoredHeader(name string, value any, guaranteed bool) error {
	spec, err := solaceheaders.Lookup(name)
	if err != nil {
		return err
	}
	if spec.GuaranteedOnly && !guaranteed {
		return fmt.Errorf("requires deliveryMode %q", solaceheaders.DeliveryGuaranteed)
	}
	if err := checkAuthoredTemplateSource(value); err != nil {
		return err
	}
	return spec.ValidateAuthoredValue(value)
}

func checkAuthoredTemplateSource(value any) error {
	if !solaceheaders.IsTemplate(value) {
		return nil
	}
	expr, _ := solaceheaders.RuntimeValue(value).(string)
	expr = strings.ReplaceAll(expr, legacyTaskResponsePrefix, inputPayloadPrefix)
	if _, err := sacexpr.NewContext(nil, "", nil).GetData(expr); errors.Is(err, sacexpr.ErrUnknownSource) {
		return err
	}
	return nil
}

func validateAuthoredUserProperty(name string, value any) error {
	if err := solaceheaders.ValidateUserPropertyKey(name); err != nil {
		return err
	}
	if err := checkAuthoredTemplateSource(value); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	switch value.(type) {
	case string, bool, int, int64, float64:
		return nil
	}
	return fmt.Errorf("%s: must be a string, number or boolean (got %T)", name, value)
}
