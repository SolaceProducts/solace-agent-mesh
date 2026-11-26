// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"encoding/json"
	"fmt"
)

type OperationDescription struct {
	ToolName    string          `json:"toolName"`
	OperationID string          `json:"operationId,omitempty"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Summary     string          `json:"summary,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type OperationSurvey struct {
	Operations         []OperationDescription
	ToolNames          []string
	MediaTypesDisagree []string
}

func SurveyOperations(ctx context.Context, spec []byte, allowList, denyList []string) (OperationSurvey, error) {
	survey, err := surveyOperations(ctx, spec, allowList, denyList)
	if err != nil {
		return OperationSurvey{}, err
	}
	descriptions, err := describeDeterminate(ctx, survey.determinate)
	if err != nil {
		return OperationSurvey{}, err
	}
	return OperationSurvey{Operations: descriptions, ToolNames: survey.toolNames, MediaTypesDisagree: survey.mediaTypesDisagree}, nil
}

func DescribeOperations(ctx context.Context, spec []byte, allowList, denyList []string) ([]OperationDescription, error) {
	ops, err := determinateOperations(ctx, spec, allowList, denyList)
	if err != nil {
		return nil, err
	}
	return describeDeterminate(ctx, ops)
}

func describeDeterminate(ctx context.Context, ops []determinateOperation) ([]OperationDescription, error) {
	descriptions := make([]OperationDescription, 0, len(ops))
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parameters, err := json.Marshal(buildParameters(buildOperationTemplate(op.info)))
		if err != nil {
			return nil, fmt.Errorf("parameters of %s: %w", op.toolName, err)
		}
		descriptions = append(descriptions, OperationDescription{
			ToolName:    op.toolName,
			OperationID: op.info.OperationID,
			Method:      op.info.Method,
			Path:        op.info.Path,
			Summary:     operationDescriptionText(op.info),
			Parameters:  parameters,
		})
	}
	return descriptions, nil
}
