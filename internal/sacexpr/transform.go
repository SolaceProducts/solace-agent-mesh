// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"fmt"
	"strings"
)

type TransformConfig struct {
	Type string `yaml:"type" json:"type"`

	SourceExpression string `yaml:"source_expression" json:"source_expression,omitempty"`
	SourceValue      any    `yaml:"source_value" json:"source_value,omitempty"`
	DestExpression   string `yaml:"dest_expression" json:"dest_expression,omitempty"`

	SourceListExpression string `yaml:"source_list_expression" json:"source_list_expression,omitempty"`
	DestListExpression   string `yaml:"dest_list_expression" json:"dest_list_expression,omitempty"`

	ProcessingFunction *InvokeConfig `yaml:"processing_function" json:"processing_function,omitempty"`

	InitialValue        any           `yaml:"initial_value" json:"initial_value,omitempty"`
	AccumulatorFunction *InvokeConfig `yaml:"accumulator_function" json:"accumulator_function,omitempty"`

	FilterFunction *InvokeConfig `yaml:"filter_function" json:"filter_function,omitempty"`

	SourceProperty     string `yaml:"source_property" json:"source_property,omitempty"`
	DestProperty       string `yaml:"dest_property" json:"dest_property,omitempty"`
	AllowMissingSource bool   `yaml:"allow_missing_source" json:"allow_missing_source,omitempty"`
}

type InvokeConfig struct {
	Module   string       `yaml:"module" json:"module,omitempty"`
	Function string       `yaml:"function" json:"function"`
	Params   InvokeParams `yaml:"params" json:"params"`
}

type InvokeParams struct {
	Positional []any          `yaml:"positional" json:"positional,omitempty"`
	Keyword    map[string]any `yaml:"keyword" json:"keyword,omitempty"`
}

func ApplyTransforms(ctx *Context, transforms []TransformConfig) error {
	for i, t := range transforms {
		if err := applyTransform(ctx, &t); err != nil {
			return fmt.Errorf("transform[%d] (%s): %w", i, t.Type, err)
		}
	}
	return nil
}

func applyTransform(ctx *Context, t *TransformConfig) error {
	switch t.Type {
	case "copy":
		return applyCopy(ctx, t)
	case "append":
		return applyAppend(ctx, t)
	case "map":
		return applyMap(ctx, t)
	case "reduce":
		return applyReduce(ctx, t)
	case "filter":
		return applyFilter(ctx, t)
	case "copy_list_item":
		return applyCopyListItem(ctx, t)
	default:
		return fmt.Errorf("unknown transform type: %q", t.Type)
	}
}

func applyCopy(ctx *Context, t *TransformConfig) error {
	var val any
	var err error

	if t.SourceValue != nil {
		val = t.SourceValue
	} else if t.SourceExpression != "" {
		val, err = ctx.GetData(t.SourceExpression)
		if err != nil {
			return fmt.Errorf("source: %w", err)
		}
	}

	if t.DestExpression == "" {
		return fmt.Errorf("copy: dest_expression required")
	}
	return ctx.SetData(t.DestExpression, val)
}

func applyAppend(ctx *Context, t *TransformConfig) error {
	val, err := ctx.GetData(t.SourceExpression)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}

	if t.DestExpression == "" {
		return fmt.Errorf("append: dest_expression required")
	}

	existing, _ := ctx.GetData(t.DestExpression)

	switch list := existing.(type) {
	case []any:
		return ctx.SetData(t.DestExpression, append(list, val))
	case nil:
		return ctx.SetData(t.DestExpression, []any{val})
	default:
		return ctx.SetData(t.DestExpression, []any{val})
	}
}

func applyMap(ctx *Context, t *TransformConfig) error {
	if t.SourceListExpression == "" {
		return fmt.Errorf("map: source_list_expression required")
	}

	srcList, err := ctx.GetData(t.SourceListExpression)
	if err != nil {
		return fmt.Errorf("source_list: %w", err)
	}
	list, ok := srcList.([]any)
	if !ok {
		if srcList == nil {
			return ctx.SetData(t.DestListExpression, []any{})
		}
		return fmt.Errorf("map: source_list is not an array, got %T", srcList)
	}

	destExpr := t.DestListExpression
	if destExpr == "" {
		destExpr = t.DestExpression
	}
	if destExpr == "" {
		return fmt.Errorf("map: dest_list_expression or dest_expression required")
	}

	result := make([]any, 0, len(list))

	for i, item := range list {
		ctx.SetIterationItem(item)
		ctx.SetKeywordArgs(map[string]any{
			"index":         i,
			"current_value": item,
			"source_list":   list,
		})

		var val any

		srcExpr := t.SourceExpression
		switch srcExpr {
		case "", "item":
			val = item
		case "index":
			val = i
		default:
			val, err = ctx.GetData(srcExpr)
			if err != nil {
				return fmt.Errorf("map item[%d] source: %w", i, err)
			}
		}

		if t.ProcessingFunction != nil {
			ctx.keywordArgs["current_value"] = val
			val, err = callInvokeWithContext(ctx, t.ProcessingFunction)
			if err != nil {
				return fmt.Errorf("map item[%d] processing: %w", i, err)
			}
		}

		if t.DestExpression != "" && t.DestListExpression != "" {
			wrapped := make(map[string]any)
			_, err = setPath(wrapped, t.DestExpression, val)
			if err != nil {
				return fmt.Errorf("map item[%d] dest: %w", i, err)
			}
			result = append(result, wrapped)
		} else {
			result = append(result, val)
		}
	}

	ctx.SetIterationItem(nil)
	ctx.ClearKeywordArgs()

	return ctx.SetData(destExpr, result)
}

func applyReduce(ctx *Context, t *TransformConfig) error {
	if t.SourceListExpression == "" {
		return fmt.Errorf("reduce: source_list_expression required")
	}
	if t.AccumulatorFunction == nil {
		return fmt.Errorf("reduce: accumulator_function required")
	}

	srcList, err := ctx.GetData(t.SourceListExpression)
	if err != nil {
		return fmt.Errorf("source_list: %w", err)
	}
	list, ok := srcList.([]any)
	if !ok {
		if srcList == nil {
			list = []any{}
		} else {
			return fmt.Errorf("reduce: source_list is not an array, got %T", srcList)
		}
	}

	accumulated := t.InitialValue

	for i, item := range list {
		ctx.SetIterationItem(item)

		var val any
		srcExpr := t.SourceExpression
		if srcExpr == "" || srcExpr == "item" {
			val = item
		} else {
			val, err = ctx.GetData(srcExpr)
			if err != nil {
				return fmt.Errorf("reduce item[%d] source: %w", i, err)
			}
		}

		ctx.SetKeywordArgs(map[string]any{
			"index":             i,
			"current_value":     val,
			"accumulated_value": accumulated,
			"source_list":       list,
		})

		accumulated, err = callInvokeWithContext(ctx, t.AccumulatorFunction)
		if err != nil {
			return fmt.Errorf("reduce item[%d] accumulator: %w", i, err)
		}
	}

	ctx.SetIterationItem(nil)
	ctx.ClearKeywordArgs()

	if t.DestExpression == "" {
		return fmt.Errorf("reduce: dest_expression required")
	}
	return ctx.SetData(t.DestExpression, accumulated)
}

func applyFilter(ctx *Context, t *TransformConfig) error {
	if t.SourceListExpression == "" {
		return fmt.Errorf("filter: source_list_expression required")
	}
	if t.FilterFunction == nil {
		return fmt.Errorf("filter: filter_function required")
	}

	srcList, err := ctx.GetData(t.SourceListExpression)
	if err != nil {
		return fmt.Errorf("source_list: %w", err)
	}
	list, ok := srcList.([]any)
	if !ok {
		if srcList == nil {
			list = []any{}
		} else {
			return fmt.Errorf("filter: source_list is not an array, got %T", srcList)
		}
	}

	destExpr := t.DestListExpression
	if destExpr == "" {
		destExpr = t.DestExpression
	}
	if destExpr == "" {
		return fmt.Errorf("filter: dest_list_expression or dest_expression required")
	}

	result := make([]any, 0)

	for i, item := range list {
		ctx.SetIterationItem(item)

		var val any
		srcExpr := t.SourceExpression
		if srcExpr == "" || srcExpr == "item" {
			val = item
		} else {
			val, err = ctx.GetData(srcExpr)
			if err != nil {
				return fmt.Errorf("filter item[%d] source: %w", i, err)
			}
		}

		ctx.SetKeywordArgs(map[string]any{
			"index":         i,
			"current_value": val,
			"source_list":   list,
		})

		keep, err := callInvokeWithContext(ctx, t.FilterFunction)
		if err != nil {
			return fmt.Errorf("filter item[%d] predicate: %w", i, err)
		}

		if isTruthy(keep) {
			result = append(result, item)
		}
	}

	ctx.SetIterationItem(nil)
	ctx.ClearKeywordArgs()

	return ctx.SetData(destExpr, result)
}

func applyCopyListItem(ctx *Context, t *TransformConfig) error {
	if t.SourceExpression == "" {
		return fmt.Errorf("copy_list_item: source_expression required")
	}

	srcList, err := ctx.GetData(t.SourceExpression)
	if err != nil {
		if t.AllowMissingSource {
			return nil
		}
		return fmt.Errorf("source: %w", err)
	}
	if srcList == nil {
		if t.AllowMissingSource {
			return nil
		}
		return fmt.Errorf("copy_list_item: source is nil")
	}

	list, ok := srcList.([]any)
	if !ok {
		return fmt.Errorf("copy_list_item: source is not an array, got %T", srcList)
	}

	destExpr := t.DestExpression
	if destExpr == "" {
		return fmt.Errorf("copy_list_item: dest_expression required")
	}

	result := make([]any, 0, len(list))

	for _, item := range list {
		var val any
		if t.SourceProperty != "" {
			val = lookupKey(item, t.SourceProperty)
		} else {
			val = item
		}

		if t.DestProperty != "" {
			wrapped := make(map[string]any)
			wrapped[t.DestProperty] = val
			result = append(result, wrapped)
		} else {
			result = append(result, val)
		}
	}

	return ctx.SetData(destExpr, result)
}

func callInvokeWithContext(ctx *Context, cfg *InvokeConfig) (any, error) {
	args, err := resolveInvokeParams(ctx, &cfg.Params)
	if err != nil {
		return nil, fmt.Errorf("resolving params for %q: %w", cfg.Function, err)
	}

	return CallInvoke(cfg.Function, args)
}

func resolveInvokeParams(ctx *Context, params *InvokeParams) ([]any, error) {
	var args []any

	for _, p := range params.Positional {
		resolved, err := resolveParam(ctx, p)
		if err != nil {
			return nil, err
		}
		args = append(args, resolved)
	}

	if len(args) == 0 && len(params.Keyword) > 0 {
		for _, v := range params.Keyword {
			resolved, err := resolveParam(ctx, v)
			if err != nil {
				return nil, err
			}
			args = append(args, resolved)
		}
	}

	return args, nil
}

func resolveParam(ctx *Context, param any) (any, error) {
	s, ok := param.(string)
	if !ok {
		return param, nil
	}

	if strings.HasPrefix(s, "evaluate_expression(") && strings.HasSuffix(s, ")") {
		inner := s[len("evaluate_expression(") : len(s)-1]

		if strings.HasPrefix(inner, "keyword_args:") {
			name := inner[len("keyword_args:"):]

			dotIdx := strings.IndexByte(name, '.')
			if dotIdx >= 0 {
				argName := name[:dotIdx]
				subPath := name[dotIdx+1:]
				val, ok := ctx.GetKeywordArg(argName)
				if !ok {
					return nil, nil
				}
				return getPath(val, subPath)
			}

			val, _ := ctx.GetKeywordArg(name)
			return val, nil
		}

		return ctx.GetData(inner)
	}

	return s, nil
}
