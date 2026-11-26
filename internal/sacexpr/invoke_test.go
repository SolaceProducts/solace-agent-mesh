// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"testing"
	"time"
)

func TestInvoke_Arithmetic(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		args []any
		want float64
	}{
		{"add ints", "add", []any{3, 4}, 7},
		{"add floats", "add", []any{1.5, 2.5}, 4},
		{"subtract", "subtract", []any{10, 3}, 7},
		{"multiply", "multiply", []any{4, 5}, 20},
		{"divide", "divide", []any{10, 4}, 2.5},
		{"modulus", "modulus", []any{10, 3}, 1},
		{"power", "power", []any{2, 10}, 1024},
		{"negate", "negate", []any{5}, -5},
		{"negate negative", "negate", []any{-3}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CallInvoke(tt.fn, tt.args)
			if err != nil {
				t.Fatalf("CallInvoke(%q): %v", tt.fn, err)
			}
			gotFloat, err := toFloat64(got)
			if err != nil {
				t.Fatalf("result not numeric: %v (%T)", got, got)
			}
			if gotFloat != tt.want {
				t.Errorf("got %v, want %v", gotFloat, tt.want)
			}
		})
	}
}

func TestInvoke_AddStrings(t *testing.T) {
	got, err := CallInvoke("add", []any{"hello", " world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %v, want 'hello world'", got)
	}
}

func TestInvoke_DivideByZero(t *testing.T) {
	_, err := CallInvoke("divide", []any{10, 0})
	if err == nil {
		t.Fatal("expected error for division by zero")
	}
}

func TestInvoke_ModulusByZero(t *testing.T) {
	_, err := CallInvoke("modulus", []any{10, 0})
	if err == nil {
		t.Fatal("expected error for modulus by zero")
	}
}

func TestInvoke_Comparison(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		args []any
		want bool
	}{
		{"equal true", "equal", []any{5, 5}, true},
		{"equal false", "equal", []any{5, 6}, false},
		{"equal string", "equal", []any{"a", "a"}, true},
		{"equal int float", "equal", []any{5, 5.0}, true},
		{"equal lists of int and float", "equal", []any{[]any{1, 2}, []any{1.0, 2.0}}, true},
		{"equal lists of int64 and float", "equal", []any{[]any{int64(1)}, []any{1.0}}, true},
		{"equal lists with a different number", "equal", []any{[]any{1, 2}, []any{1.0, 3.0}}, false},
		{"equal lists of different length", "equal", []any{[]any{1}, []any{1.0, 2.0}}, false},
		{"equal nested lists of int and float", "equal", []any{[]any{[]any{1}}, []any{[]any{1.0}}}, true},
		{"equal objects holding int and float", "equal", []any{map[string]any{"a": 1}, map[string]any{"a": 1.0}}, true},
		{"equal objects with a different number", "equal", []any{map[string]any{"a": 1}, map[string]any{"a": 2.0}}, false},
		{"equal objects with different keys", "equal", []any{map[string]any{"a": 1}, map[string]any{"b": 1.0}}, false},
		{"equal a longer left list against a shorter right list", "equal", []any{[]any{1, 2, 3}, []any{1.0, 2.0}}, false},
		{"equal a shorter left list against a longer right list", "equal", []any{[]any{1.0, 2.0}, []any{1, 2, 3}}, false},
		{"equal a left object with more keys", "equal", []any{map[string]any{"a": 1, "b": 2}, map[string]any{"a": 1.0}}, false},
		{"equal a right object with more keys", "equal", []any{map[string]any{"a": 1.0}, map[string]any{"a": 1, "b": 2}}, false},
		{"equal objects with different keys both holding null", "equal", []any{map[string]any{"a": nil}, map[string]any{"b": nil}}, false},
		{"equal objects with different keys both holding null, swapped", "equal", []any{map[string]any{"b": nil}, map[string]any{"a": nil}}, false},
		{"equal lists of objects with different keys both holding null", "equal", []any{[]any{map[string]any{"a": nil}}, []any{map[string]any{"b": nil}}}, false},
		{"not_equal objects with different keys both holding null", "not_equal", []any{map[string]any{"a": nil}, map[string]any{"b": nil}}, true},
		{"not_equal lists of objects with different keys both holding null", "not_equal", []any{[]any{map[string]any{"a": nil}}, []any{map[string]any{"b": nil}}}, true},
		{"equal an empty list against null", "equal", []any{[]any{}, nil}, false},
		{"equal an empty object against text", "equal", []any{map[string]any{}, "x"}, false},
		{"not_equal lists of int and float", "not_equal", []any{[]any{1, 2}, []any{1.0, 2.0}}, false},
		{"not_equal true", "not_equal", []any{5, 6}, true},
		{"not_equal false", "not_equal", []any{5, 5}, false},
		{"greater_than true", "greater_than", []any{10, 5}, true},
		{"greater_than false", "greater_than", []any{5, 10}, false},
		{"greater_than equal", "greater_than", []any{5, 5}, false},
		{"less_than true", "less_than", []any{5, 10}, true},
		{"less_than false", "less_than", []any{10, 5}, false},
		{"gte true eq", "greater_than_or_equal", []any{5, 5}, true},
		{"gte true gt", "greater_than_or_equal", []any{6, 5}, true},
		{"gte false", "greater_than_or_equal", []any{4, 5}, false},
		{"lte true eq", "less_than_or_equal", []any{5, 5}, true},
		{"lte true lt", "less_than_or_equal", []any{4, 5}, true},
		{"lte false", "less_than_or_equal", []any{6, 5}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CallInvoke(tt.fn, tt.args)
			if err != nil {
				t.Fatalf("CallInvoke(%q): %v", tt.fn, err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func nestedLists(levels int) any {
	var value any = []any{}
	for range levels {
		value = []any{value}
	}
	return value
}

func nestedObjects(levels int) any {
	var value any = map[string]any{}
	for range levels {
		value = map[string]any{"k": value}
	}
	return value
}

func TestInvoke_ComparisonBoundsNestingDepth(t *testing.T) {
	selfList := []any{nil}
	selfList[0] = selfList
	selfObject := map[string]any{}
	selfObject["k"] = selfObject

	tests := []struct {
		name    string
		fn      string
		args    []any
		want    bool
		wantErr bool
	}{
		{"equal on a list that contains itself", "equal", []any{selfList, selfList}, false, true},
		{"not_equal on a list that contains itself", "not_equal", []any{selfList, selfList}, false, true},
		{"equal on an object that contains itself", "equal", []any{selfObject, selfObject}, false, true},
		{"in_op on a list that contains itself", "in_op", []any{selfList, []any{selfList}}, false, true},
		{"equal on lists nested past the bound", "equal", []any{nestedLists(maxCompareDepth + 1), nestedLists(maxCompareDepth + 1)}, false, true},
		{"equal on lists nested exactly to the bound", "equal", []any{nestedLists(maxCompareDepth), nestedLists(maxCompareDepth)}, true, false},
		{"equal on objects nested exactly to the bound", "equal", []any{nestedObjects(maxCompareDepth), nestedObjects(maxCompareDepth)}, true, false},
		{"equal on deep lists that differ in length before the bound", "equal", []any{nestedLists(maxCompareDepth + 5), []any{}}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CallInvoke(tt.fn, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CallInvoke(%q) error = %v, wantErr %v", tt.fn, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("CallInvoke(%q) = %v, want %v", tt.fn, got, tt.want)
			}
		})
	}
}

func sharedDoublingLists(levels int) any {
	var value any = []any{}
	for range levels {
		value = []any{value, value}
	}
	return value
}

func sharedDoublingObjects(levels int) any {
	var value any = map[string]any{}
	for range levels {
		value = map[string]any{"left": value, "right": value}
	}
	return value
}

func flatList(items int) any {
	list := make([]any, items)
	for i := range list {
		list[i] = 1.0
	}
	return list
}

func callWithinTime(t *testing.T, fn string, args []any) (any, error) {
	t.Helper()
	type outcome struct {
		got any
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		got, err := CallInvoke(fn, args)
		done <- outcome{got, err}
	}()
	select {
	case o := <-done:
		return o.got, o.err
	case <-time.After(30 * time.Second):
		t.Fatalf("CallInvoke(%q) still running after 30s", fn)
		return nil, nil
	}
}

func TestInvoke_ComparisonBoundsTheWorkDone(t *testing.T) {
	tests := []struct {
		name    string
		fn      string
		args    []any
		want    bool
		wantErr bool
	}{
		{"equal on lists whose children are shared at every level", "equal", []any{sharedDoublingLists(60), sharedDoublingLists(60)}, false, true},
		{"not_equal on lists whose children are shared at every level", "not_equal", []any{sharedDoublingLists(60), sharedDoublingLists(60)}, false, true},
		{"equal on objects whose children are shared at every level", "equal", []any{sharedDoublingObjects(60), sharedDoublingObjects(60)}, false, true},
		{"in_op on a list item whose children are shared at every level", "in_op", []any{sharedDoublingLists(60), []any{sharedDoublingLists(60)}}, false, true},
		{"equal on shared children small enough to finish", "equal", []any{sharedDoublingLists(15), sharedDoublingLists(15)}, true, false},
		{"equal on a flat list that uses the whole budget", "equal", []any{flatList(maxCompareSteps - 1), flatList(maxCompareSteps - 1)}, true, false},
		{"equal on a flat list one value past the budget", "equal", []any{flatList(maxCompareSteps), flatList(maxCompareSteps)}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := callWithinTime(t, tt.fn, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CallInvoke(%q) error = %v, wantErr %v", tt.fn, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("CallInvoke(%q) = %v, want %v", tt.fn, got, tt.want)
			}
		})
	}
}

func TestInvoke_StringComparison(t *testing.T) {
	got, err := CallInvoke("greater_than", []any{"b", "a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != true {
		t.Errorf("expected 'b' > 'a' to be true")
	}
}

func TestInvoke_Logical(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		args []any
		want bool
	}{
		{"and true", "and_op", []any{true, true}, true},
		{"and false", "and_op", []any{true, false}, false},
		{"and truthy", "and_op", []any{"nonempty", 1}, true},
		{"or true", "or_op", []any{false, true}, true},
		{"or false", "or_op", []any{false, false}, false},
		{"or truthy", "or_op", []any{"", "hello"}, true},
		{"not true", "not_op", []any{false}, true},
		{"not false", "not_op", []any{true}, false},
		{"not nil", "not_op", []any{nil}, true},
		{"not empty string", "not_op", []any{""}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CallInvoke(tt.fn, tt.args)
			if err != nil {
				t.Fatalf("CallInvoke(%q): %v", tt.fn, err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInvoke_Container(t *testing.T) {
	t.Run("in_op list found", func(t *testing.T) {
		got, err := CallInvoke("in_op", []any{"b", []any{"a", "b", "c"}})
		if err != nil {
			t.Fatal(err)
		}
		if got != true {
			t.Error("expected true")
		}
	})

	t.Run("in_op list not found", func(t *testing.T) {
		got, err := CallInvoke("in_op", []any{"d", []any{"a", "b", "c"}})
		if err != nil {
			t.Fatal(err)
		}
		if got != false {
			t.Error("expected false")
		}
	})

	t.Run("in_op string", func(t *testing.T) {
		got, err := CallInvoke("in_op", []any{"ell", "hello"})
		if err != nil {
			t.Fatal(err)
		}
		if got != true {
			t.Error("expected true")
		}
	})

	t.Run("in_op map", func(t *testing.T) {
		got, err := CallInvoke("in_op", []any{"key", map[string]any{"key": "val"}})
		if err != nil {
			t.Fatal(err)
		}
		if got != true {
			t.Error("expected true")
		}
	})

	t.Run("in_op string map key present", func(t *testing.T) {
		got, err := CallInvoke("in_op", []any{"region", map[string]string{"region": "eu"}})
		if err != nil || got != true {
			t.Errorf("got %v, %v; want true, nil", got, err)
		}
	})

	t.Run("in_op string map key absent", func(t *testing.T) {
		got, err := CallInvoke("in_op", []any{"tier", map[string]string{"region": "eu"}})
		if err != nil || got != false {
			t.Errorf("got %v, %v; want false, nil", got, err)
		}
	})

	t.Run("append", func(t *testing.T) {
		got, err := CallInvoke("append", []any{[]any{1, 2}, 3})
		if err != nil {
			t.Fatal(err)
		}
		list := got.([]any)
		if len(list) != 3 || list[2] != 3 {
			t.Errorf("got %v, want [1 2 3]", list)
		}
	})
}

func TestInvoke_IfElse(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want any
	}{
		{"true branch", []any{true, "yes", "no"}, "yes"},
		{"false branch", []any{false, "yes", "no"}, "no"},
		{"truthy", []any{1, "yes", "no"}, "yes"},
		{"falsy nil", []any{nil, "yes", "no"}, "no"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CallInvoke("if_else", tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInvoke_Constructors(t *testing.T) {
	tests := []struct {
		name string
		fn   string
	}{
		{"empty_list", "empty_list"},
		{"empty_dict", "empty_dict"},
		{"empty_string", "empty_string"},
		{"empty_int", "empty_int"},
		{"empty_float", "empty_float"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CallInvoke(tt.fn, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got == nil {
				t.Error("expected non-nil")
			}
		})
	}
}

func TestInvoke_UUID(t *testing.T) {
	got, err := CallInvoke("uuid", nil)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := got.(string)
	if !ok {
		t.Fatalf("expected string, got %T", got)
	}
	if len(s) != 36 {
		t.Errorf("expected UUID length 36, got %d: %q", len(s), s)
	}
}

func TestInvoke_UnknownFunction(t *testing.T) {
	_, err := CallInvoke("nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown function")
	}
}

func TestInvoke_InsufficientArgs(t *testing.T) {
	_, err := CallInvoke("add", []any{1})
	if err == nil {
		t.Fatal("expected error for insufficient args")
	}
}

func TestIsTruthy(t *testing.T) {
	tests := []struct {
		val  any
		want bool
	}{
		{nil, false},
		{false, false},
		{true, true},
		{"", false},
		{"hello", true},
		{0, false},
		{1, true},
		{0.0, false},
		{1.5, true},
		{[]any{}, false},
		{[]any{1}, true},
		{map[string]any{}, false},
		{map[string]any{"k": "v"}, true},
	}

	for _, tt := range tests {
		got := isTruthy(tt.val)
		if got != tt.want {
			t.Errorf("isTruthy(%v) = %v, want %v", tt.val, got, tt.want)
		}
	}
}
