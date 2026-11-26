// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"testing"
)

func TestTransform_Copy_Simple(t *testing.T) {
	ctx := NewContext(map[string]any{"name": "alice"}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:             "copy",
			SourceExpression: "input.payload:name",
			DestExpression:   "user_data.output:username",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.output:username")
	if got != "alice" {
		t.Errorf("got %v, want alice", got)
	}
}

func TestTransform_Copy_StaticValue(t *testing.T) {
	ctx := NewContext(nil, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:           "copy",
			SourceValue:    "hello world",
			DestExpression: "user_data.temp:greeting",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.temp:greeting")
	if got != "hello world" {
		t.Errorf("got %v, want 'hello world'", got)
	}
}

func TestTransform_Copy_NestedPath(t *testing.T) {
	ctx := NewContext(map[string]any{
		"user": map[string]any{"name": "bob"},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:             "copy",
			SourceExpression: "input.payload:user.name",
			DestExpression:   "user_data.result:extracted_name",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:extracted_name")
	if got != "bob" {
		t.Errorf("got %v, want bob", got)
	}
}

func TestTransform_Copy_CrossSection(t *testing.T) {
	ctx := NewContext(map[string]any{"a": 1}, "", nil)
	_ = ctx.SetData("user_data.src:val", "from_userdata")

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:             "copy",
			SourceExpression: "user_data.src:val",
			DestExpression:   "input.payload:b",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("input.payload:b")
	if got != "from_userdata" {
		t.Errorf("got %v, want from_userdata", got)
	}
}

func TestTransform_Copy_MissingDest(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	err := ApplyTransforms(ctx, []TransformConfig{
		{Type: "copy", SourceValue: "x"},
	})
	if err == nil {
		t.Fatal("expected error for missing dest_expression")
	}
}

func TestTransform_Append_NewList(t *testing.T) {
	ctx := NewContext(map[string]any{"val": "a"}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{Type: "append", SourceExpression: "input.payload:val", DestExpression: "user_data.temp:list"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.temp:list")
	assertDeepEqual(t, got, []any{"a"})
}

func TestTransform_Append_ExistingList(t *testing.T) {
	ctx := NewContext(map[string]any{"one": 1, "two": 2}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{Type: "append", SourceExpression: "input.payload:one", DestExpression: "user_data.temp:list"},
		{Type: "append", SourceExpression: "input.payload:two", DestExpression: "user_data.temp:list"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.temp:list")
	assertDeepEqual(t, got, []any{1, 2})
}

func TestTransform_Append_OverwriteNonList(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	_ = ctx.SetData("user_data.temp:val", "not a list")

	err := ApplyTransforms(ctx, []TransformConfig{
		{Type: "append", SourceExpression: "static:item", DestExpression: "user_data.temp:val"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.temp:val")
	assertDeepEqual(t, got, []any{"item"})
}

func TestTransform_Map_SimpleExtraction(t *testing.T) {
	ctx := NewContext(map[string]any{
		"items": []any{
			map[string]any{"id": 1, "name": "a"},
			map[string]any{"id": 2, "name": "b"},
			map[string]any{"id": 3, "name": "c"},
		},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "map",
			SourceListExpression: "input.payload:items",
			SourceExpression:     "list_item:name",
			DestListExpression:   "user_data.result:names",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:names")
	assertDeepEqual(t, got, []any{"a", "b", "c"})
}

func TestTransform_Map_WithInvokeFunction(t *testing.T) {
	ctx := NewContext(map[string]any{
		"numbers": []any{1, 2, 3},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "map",
			SourceListExpression: "input.payload:numbers",
			DestListExpression:   "user_data.result:doubled",
			ProcessingFunction: &InvokeConfig{
				Function: "multiply",
				Params: InvokeParams{
					Positional: []any{
						"evaluate_expression(keyword_args:current_value)",
						2,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:doubled")
	assertDeepEqual(t, got, []any{2.0, 4.0, 6.0})
}

func TestTransform_Map_DefaultItem(t *testing.T) {
	ctx := NewContext(map[string]any{
		"items": []any{"x", "y"},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "map",
			SourceListExpression: "input.payload:items",
			DestListExpression:   "user_data.result:copied",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:copied")
	assertDeepEqual(t, got, []any{"x", "y"})
}

func TestTransform_Map_EmptyList(t *testing.T) {
	ctx := NewContext(map[string]any{
		"items": []any{},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "map",
			SourceListExpression: "input.payload:items",
			DestListExpression:   "user_data.result:empty",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:empty")
	assertDeepEqual(t, got, []any{})
}

func TestTransform_Map_NilList(t *testing.T) {
	ctx := NewContext(map[string]any{}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "map",
			SourceListExpression: "input.payload:missing",
			DestListExpression:   "user_data.result:empty",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:empty")
	assertDeepEqual(t, got, []any{})
}

func TestTransform_Map_IndexExpression(t *testing.T) {
	ctx := NewContext(map[string]any{
		"items": []any{"a", "b", "c"},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "map",
			SourceListExpression: "input.payload:items",
			SourceExpression:     "index",
			DestListExpression:   "user_data.result:indices",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:indices")
	assertDeepEqual(t, got, []any{0, 1, 2})
}

func TestTransform_Reduce_Sum(t *testing.T) {
	ctx := NewContext(map[string]any{
		"numbers": []any{1, 2, 3, 4, 5},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "reduce",
			SourceListExpression: "input.payload:numbers",
			InitialValue:         0,
			DestExpression:       "user_data.result:sum",
			AccumulatorFunction: &InvokeConfig{
				Function: "add",
				Params: InvokeParams{
					Positional: []any{
						"evaluate_expression(keyword_args:accumulated_value)",
						"evaluate_expression(keyword_args:current_value)",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:sum")
	gotFloat, _ := toFloat64(got)
	if gotFloat != 15.0 {
		t.Errorf("got %v, want 15", got)
	}
}

func TestTransform_Reduce_WithSourceExpression(t *testing.T) {
	ctx := NewContext(map[string]any{
		"items": []any{
			map[string]any{"value": 10},
			map[string]any{"value": 20},
			map[string]any{"value": 30},
		},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "reduce",
			SourceListExpression: "input.payload:items",
			SourceExpression:     "list_item:value",
			InitialValue:         0,
			DestExpression:       "user_data.result:total",
			AccumulatorFunction: &InvokeConfig{
				Function: "add",
				Params: InvokeParams{
					Positional: []any{
						"evaluate_expression(keyword_args:accumulated_value)",
						"evaluate_expression(keyword_args:current_value)",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:total")
	gotFloat, _ := toFloat64(got)
	if gotFloat != 60.0 {
		t.Errorf("got %v, want 60", got)
	}
}

func TestTransform_Reduce_EmptyList(t *testing.T) {
	ctx := NewContext(map[string]any{
		"items": []any{},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "reduce",
			SourceListExpression: "input.payload:items",
			InitialValue:         "start",
			DestExpression:       "user_data.result:val",
			AccumulatorFunction: &InvokeConfig{
				Function: "add",
				Params: InvokeParams{
					Positional: []any{
						"evaluate_expression(keyword_args:accumulated_value)",
						"evaluate_expression(keyword_args:current_value)",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:val")
	if got != "start" {
		t.Errorf("got %v, want 'start'", got)
	}
}

func TestTransform_Filter_ByComparison(t *testing.T) {
	ctx := NewContext(map[string]any{
		"numbers": []any{1, 5, 2, 8, 3, 9},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "filter",
			SourceListExpression: "input.payload:numbers",
			DestListExpression:   "user_data.result:filtered",
			FilterFunction: &InvokeConfig{
				Function: "greater_than",
				Params: InvokeParams{
					Positional: []any{
						"evaluate_expression(keyword_args:current_value)",
						4,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:filtered")
	assertDeepEqual(t, got, []any{5, 8, 9})
}

func TestTransform_Filter_WithSourceExpression(t *testing.T) {
	ctx := NewContext(map[string]any{
		"items": []any{
			map[string]any{"name": "a", "active": true},
			map[string]any{"name": "b", "active": false},
			map[string]any{"name": "c", "active": true},
		},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "filter",
			SourceListExpression: "input.payload:items",
			SourceExpression:     "list_item:active",
			DestListExpression:   "user_data.result:active_items",
			FilterFunction: &InvokeConfig{
				Function: "equal",
				Params: InvokeParams{
					Positional: []any{
						"evaluate_expression(keyword_args:current_value)",
						true,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:active_items")
	list := got.([]any)
	if len(list) != 2 {
		t.Errorf("got %d items, want 2", len(list))
	}
}

func TestTransform_Filter_EmptyResult(t *testing.T) {
	ctx := NewContext(map[string]any{
		"numbers": []any{1, 2, 3},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "filter",
			SourceListExpression: "input.payload:numbers",
			DestListExpression:   "user_data.result:filtered",
			FilterFunction: &InvokeConfig{
				Function: "greater_than",
				Params: InvokeParams{
					Positional: []any{
						"evaluate_expression(keyword_args:current_value)",
						100,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:filtered")
	assertDeepEqual(t, got, []any{})
}

func TestTransform_CopyListItem_SimpleProperty(t *testing.T) {
	ctx := NewContext(map[string]any{
		"items": []any{
			map[string]any{"id": 1, "name": "a"},
			map[string]any{"id": 2, "name": "b"},
		},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:             "copy_list_item",
			SourceExpression: "input.payload:items",
			SourceProperty:   "name",
			DestExpression:   "user_data.result:names",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:names")
	assertDeepEqual(t, got, []any{"a", "b"})
}

func TestTransform_CopyListItem_WithDestProperty(t *testing.T) {
	ctx := NewContext(map[string]any{
		"items": []any{
			map[string]any{"id": 1},
			map[string]any{"id": 2},
		},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:             "copy_list_item",
			SourceExpression: "input.payload:items",
			SourceProperty:   "id",
			DestExpression:   "user_data.result:wrapped",
			DestProperty:     "item_id",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:wrapped")
	assertDeepEqual(t, got, []any{
		map[string]any{"item_id": 1},
		map[string]any{"item_id": 2},
	})
}

func TestTransform_CopyListItem_AllowMissing(t *testing.T) {
	ctx := NewContext(map[string]any{}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:               "copy_list_item",
			SourceExpression:   "input.payload:missing",
			DestExpression:     "user_data.result:items",
			AllowMissingSource: true,
		},
	})
	if err != nil {
		t.Fatal("expected no error with allow_missing_source")
	}
}

func TestTransform_CopyListItem_NotAllowMissing(t *testing.T) {
	ctx := NewContext(map[string]any{}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:             "copy_list_item",
			SourceExpression: "input.payload:missing",
			DestExpression:   "user_data.result:items",
		},
	})
	if err == nil {
		t.Fatal("expected error for missing source without allow_missing_source")
	}
}

func TestTransform_Pipeline_CopyThenMap(t *testing.T) {
	ctx := NewContext(map[string]any{
		"data": map[string]any{
			"items": []any{
				map[string]any{"val": 10},
				map[string]any{"val": 20},
			},
		},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:             "copy",
			SourceExpression: "input.payload:data.items",
			DestExpression:   "user_data.temp:items",
		},
		{
			Type:                 "map",
			SourceListExpression: "user_data.temp:items",
			SourceExpression:     "list_item:val",
			DestListExpression:   "user_data.result:values",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:values")
	assertDeepEqual(t, got, []any{10, 20})
}

func TestTransform_Pipeline_FilterThenReduce(t *testing.T) {
	ctx := NewContext(map[string]any{
		"scores": []any{10, 25, 5, 30, 15},
	}, "", nil)

	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "filter",
			SourceListExpression: "input.payload:scores",
			DestListExpression:   "user_data.temp:high_scores",
			FilterFunction: &InvokeConfig{
				Function: "greater_than",
				Params: InvokeParams{
					Positional: []any{
						"evaluate_expression(keyword_args:current_value)",
						10,
					},
				},
			},
		},
		{
			Type:                 "reduce",
			SourceListExpression: "user_data.temp:high_scores",
			InitialValue:         0,
			DestExpression:       "user_data.result:total",
			AccumulatorFunction: &InvokeConfig{
				Function: "add",
				Params: InvokeParams{
					Positional: []any{
						"evaluate_expression(keyword_args:accumulated_value)",
						"evaluate_expression(keyword_args:current_value)",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := ctx.GetData("user_data.result:total")
	gotFloat, _ := toFloat64(got)
	if gotFloat != 70.0 {
		t.Errorf("got %v, want 70", got)
	}
}

func TestTransform_UnknownType(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	err := ApplyTransforms(ctx, []TransformConfig{
		{Type: "nonexistent"},
	})
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestTransform_Map_MissingSourceList(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	err := ApplyTransforms(ctx, []TransformConfig{
		{Type: "map", DestListExpression: "user_data.r:x"},
	})
	if err == nil {
		t.Fatal("expected error for missing source_list_expression")
	}
}

func TestTransform_Reduce_MissingAccumulator(t *testing.T) {
	ctx := NewContext(map[string]any{"items": []any{1}}, "", nil)
	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "reduce",
			SourceListExpression: "input.payload:items",
			DestExpression:       "user_data.r:x",
		},
	})
	if err == nil {
		t.Fatal("expected error for missing accumulator_function")
	}
}

func TestTransform_Filter_MissingPredicate(t *testing.T) {
	ctx := NewContext(map[string]any{"items": []any{1}}, "", nil)
	err := ApplyTransforms(ctx, []TransformConfig{
		{
			Type:                 "filter",
			SourceListExpression: "input.payload:items",
			DestListExpression:   "user_data.r:x",
		},
	})
	if err == nil {
		t.Fatal("expected error for missing filter_function")
	}
}

func TestResolveParam_Literal(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	got, err := resolveParam(ctx, "just a string")
	if err != nil {
		t.Fatal(err)
	}
	if got != "just a string" {
		t.Errorf("got %v", got)
	}
}

func TestResolveParam_NonString(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	got, err := resolveParam(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Errorf("got %v", got)
	}
}

func TestResolveParam_EvaluateExpression(t *testing.T) {
	ctx := NewContext(map[string]any{"val": "hello"}, "", nil)
	got, err := resolveParam(ctx, "evaluate_expression(input.payload:val)")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("got %v, want hello", got)
	}
}

func TestResolveParam_KeywordArgs(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	ctx.SetKeywordArgs(map[string]any{
		"current_value": 42,
	})

	got, err := resolveParam(ctx, "evaluate_expression(keyword_args:current_value)")
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Errorf("got %v, want 42", got)
	}
}

func TestResolveParam_KeywordArgsDottedPath(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	ctx.SetKeywordArgs(map[string]any{
		"current_value": map[string]any{"nested": "deep"},
	})

	got, err := resolveParam(ctx, "evaluate_expression(keyword_args:current_value.nested)")
	if err != nil {
		t.Fatal(err)
	}
	if got != "deep" {
		t.Errorf("got %v, want deep", got)
	}
}
