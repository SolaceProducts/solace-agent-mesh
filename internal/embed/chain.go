// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/embed/directive"
)

type ModifierStep = directive.ModifierStep

func ParseModifierChain(expression string) (artifactSpec string, modifiers []ModifierStep, outputFormat string) {
	return directive.ParseModifierChain(expression)
}

func ExecuteChain(
	ctx context.Context,
	data any,
	format DataFormat,
	mimeType string,
	modifiers []ModifierStep,
	applyTemplate func(ctx context.Context, data any, templateSpec string, mimeType string) (string, error),
	logger *slog.Logger,
) (any, DataFormat, error) {
	currentData := data
	currentFormat := format

	for _, step := range modifiers {
		def, ok := ModifierRegistry[step.Name]
		if !ok {
			return nil, currentFormat, fmt.Errorf("unknown modifier: %s", step.Name)
		}

		if step.Name == "apply_to_template" {
			if applyTemplate == nil {
				return nil, currentFormat, fmt.Errorf("apply_to_template modifier requires artifact service context")
			}

			var convErr error
			currentData, currentFormat, convErr = autoConvert(currentData, currentFormat, def.Accepts, mimeType)
			if convErr != nil {
				return nil, currentFormat, fmt.Errorf("auto-conversion for %s failed: %w", step.Name, convErr)
			}

			result, err := applyTemplate(ctx, currentData, step.Value, mimeType)
			if err != nil {
				return nil, currentFormat, fmt.Errorf("apply_to_template failed: %w", err)
			}
			currentData = result
			currentFormat = FormatString
			continue
		}

		var err error
		currentData, currentFormat, err = autoConvert(currentData, currentFormat, def.Accepts, mimeType)
		if err != nil {
			return nil, currentFormat, fmt.Errorf("auto-conversion for %s failed: %w", step.Name, err)
		}

		result, err := def.Func(currentData, step.Value, mimeType)
		if err != nil {
			return nil, currentFormat, fmt.Errorf("modifier %s failed: %w", step.Name, err)
		}

		currentData = result
		currentFormat = def.Produces
	}

	return currentData, currentFormat, nil
}

func autoConvert(data any, current DataFormat, accepted []DataFormat, mimeType string) (any, DataFormat, error) {
	for _, f := range accepted {
		if current == f {
			return data, current, nil
		}
	}

	if len(accepted) == 0 {
		return data, current, fmt.Errorf("no accepted formats")
	}

	converted, newFormat, err := ConvertData(data, current, accepted[0], mimeType)
	if err != nil {
		return data, current, err
	}
	return converted, newFormat, nil
}
