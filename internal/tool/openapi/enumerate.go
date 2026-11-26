// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

func EnumerateToolNames(ctx context.Context, data []byte, allowList, denyList []string) ([]string, error) {
	doc, err := loadSpecWithoutExternalRefs(ctx, data)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, op := range extractOperations(doc) {
		if !shouldInclude(op.OperationID, allowList, denyList) {
			continue
		}
		names = append(names, OperationToolName(op.OperationID))
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

func loadSpecWithoutExternalRefs(ctx context.Context, data []byte) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.Context = ctx

	doc, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	return doc, nil
}

func ClashingOperationIDs(ctx context.Context, data []byte, allowList, denyList []string) ([]string, error) {
	doc, err := loadSpecWithoutExternalRefs(ctx, data)
	if err != nil {
		return nil, err
	}
	idsByToolName := map[string][]string{}
	for _, op := range extractOperations(doc) {
		if !shouldInclude(op.OperationID, allowList, denyList) {
			continue
		}
		name := OperationToolName(op.OperationID)
		idsByToolName[name] = append(idsByToolName[name], op.OperationID)
	}
	var clashing []string
	for _, ids := range idsByToolName {
		if len(ids) > 1 {
			clashing = append(clashing, ids...)
		}
	}
	slices.Sort(clashing)
	return slices.Compact(clashing), nil
}

type OperationParam struct {
	Name     string
	Required bool
}

type OperationSignature struct {
	ToolName string
	Params   []OperationParam
}

func EnumerateOperations(ctx context.Context, data []byte, allowList, denyList []string) ([]OperationSignature, error) {
	ops, err := determinateOperations(ctx, data, allowList, denyList)
	if err != nil {
		return nil, err
	}
	signatures := make([]OperationSignature, len(ops))
	for i, op := range ops {
		signatures[i] = OperationSignature{ToolName: op.toolName, Params: op.params}
	}
	return signatures, nil
}

type determinateOperation struct {
	toolName string
	info     operationInfo
	params   []OperationParam
}

type operationSurvey struct {
	determinate        []determinateOperation
	toolNames          []string
	mediaTypesDisagree []string
}

func determinateOperations(ctx context.Context, data []byte, allowList, denyList []string) ([]determinateOperation, error) {
	survey, err := surveyOperations(ctx, data, allowList, denyList)
	if err != nil {
		return nil, err
	}
	return survey.determinate, nil
}

func surveyOperations(ctx context.Context, data []byte, allowList, denyList []string) (operationSurvey, error) {
	doc, err := loadSpecWithoutExternalRefs(ctx, data)
	if err != nil {
		return operationSurvey{}, err
	}

	byName := map[string]determinateOperation{}
	ambiguous := map[string]bool{}
	disagreeing := map[string]bool{}
	var names []string
	for _, op := range extractOperations(doc) {
		if err := ctx.Err(); err != nil {
			return operationSurvey{}, err
		}
		if !shouldInclude(op.OperationID, allowList, denyList) {
			continue
		}
		name := OperationToolName(op.OperationID)
		names = append(names, name)
		if _, seen := byName[name]; seen {
			ambiguous[name] = true
			continue
		}
		params, determinate := operationParams(op)
		if !determinate {
			ambiguous[name] = true
			disagreeing[name] = true
			continue
		}
		byName[name] = determinateOperation{toolName: name, info: op, params: params}
	}

	ops := make([]determinateOperation, 0, len(byName))
	for name, op := range byName {
		if !ambiguous[name] {
			ops = append(ops, op)
		}
	}
	slices.SortFunc(ops, func(a, b determinateOperation) int { return strings.Compare(a.toolName, b.toolName) })
	slices.Sort(names)
	return operationSurvey{
		determinate:        ops,
		toolNames:          slices.Compact(names),
		mediaTypesDisagree: slices.Sorted(maps.Keys(disagreeing)),
	}, nil
}

func operationParams(op operationInfo) ([]OperationParam, bool) {
	if op.RequestBody == nil || op.RequestBody.Value == nil || declaredMediaTypes(op.RequestBody.Value.Content) < 2 {
		return paramsOf(op), true
	}
	var agreed []OperationParam
	first := true
	for mime, media := range op.RequestBody.Value.Content {
		if media == nil {
			continue
		}
		single := *op.RequestBody.Value
		single.Content = openapi3.Content{mime: media}
		variant := op
		variant.RequestBody = &openapi3.RequestBodyRef{Value: &single}
		params := paramsOf(variant)
		if first {
			agreed, first = params, false
			continue
		}
		if !sameParams(agreed, params) {
			return nil, false
		}
	}
	return agreed, true
}

func declaredMediaTypes(content openapi3.Content) int {
	n := 0
	for _, media := range content {
		if media != nil {
			n++
		}
	}
	return n
}

func paramsOf(op operationInfo) []OperationParam {
	tmpl := buildOperationTemplate(op)
	params := make([]OperationParam, len(tmpl.Params))
	for i, p := range tmpl.Params {
		params[i] = OperationParam{Name: p.PyName, Required: p.Required}
	}
	return params
}

func sameParams(a, b []OperationParam) bool {
	key := func(p OperationParam) string { return fmt.Sprintf("%s/%t", p.Name, p.Required) }
	keys := func(params []OperationParam) []string {
		out := make([]string, len(params))
		for i, p := range params {
			out[i] = key(p)
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(keys(a), keys(b))
}
