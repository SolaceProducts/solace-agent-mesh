// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCoerceString(t *testing.T) {
	t.Run("nil returns empty", func(t *testing.T) {
		got, err := coerceString("f", nil)
		if err != nil || got != "" {
			t.Fatalf("got (%q, %v)", got, err)
		}
	})
	t.Run("string passes through", func(t *testing.T) {
		got, err := coerceString("f", "hello")
		if err != nil || got != "hello" {
			t.Fatalf("got (%q, %v)", got, err)
		}
	})
	t.Run("int errors", func(t *testing.T) {
		if _, err := coerceString("f", 42); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestCoerceInt(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int
	}{
		{"int", int(7), 7},
		{"int32", int32(7), 7},
		{"int64", int64(7), 7},
		{"uint", uint(7), 7},
		{"uint32", uint32(7), 7},
		{"uint64", uint64(7), 7},
		{"float64-integral", float64(7), 7},
		{"float32-integral", float32(7), 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := coerceInt("f", tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("got (%d, %v) want %d", got, err, tc.want)
			}
		})
	}
	t.Run("non-integral float errors", func(t *testing.T) {
		if _, err := coerceInt("f", float64(1.5)); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("string errors", func(t *testing.T) {
		if _, err := coerceInt("f", "7"); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("nil returns zero", func(t *testing.T) {
		got, err := coerceInt("f", nil)
		if err != nil || got != 0 {
			t.Fatalf("got (%d, %v)", got, err)
		}
	})
}

func TestCoerceInt64(t *testing.T) {
	got, err := coerceInt64("f", float64(42))
	if err != nil || got != int64(42) {
		t.Fatalf("got (%d, %v)", got, err)
	}
	if _, err := coerceInt64("f", "x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCoerceFloat64(t *testing.T) {
	for _, in := range []any{0.8, float32(0.5), 1, int64(1), uint(0)} {
		if _, err := coerceFloat64("f", in); err != nil {
			t.Errorf("coerceFloat64(%T %v): %v", in, in, err)
		}
	}
	if got, _ := coerceFloat64("f", 0.8); got != 0.8 {
		t.Errorf("coerceFloat64(0.8) = %v", got)
	}
	for _, in := range []any{"0.8", true} {
		if _, err := coerceFloat64("f", in); err == nil {
			t.Errorf("coerceFloat64(%T %v): want an error", in, in)
		}
	}
	t.Run("nil returns zero", func(t *testing.T) {
		got, err := coerceFloat64("f", nil)
		if err != nil || got != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}

func TestCoerceBool(t *testing.T) {
	got, err := coerceBool("f", true)
	if err != nil || !got {
		t.Fatalf("got (%v, %v)", got, err)
	}
	if _, err := coerceBool("f", "true"); err == nil {
		t.Fatal("expected error for non-bool")
	}
}

func TestCoerceStringSlice(t *testing.T) {
	t.Run("[]any of strings", func(t *testing.T) {
		got, err := coerceStringSlice("f", []any{"a", "b"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if diff := cmp.Diff([]string{"a", "b"}, got); diff != "" {
			t.Fatalf("mismatch:\n%s", diff)
		}
	})
	t.Run("[]string passes through", func(t *testing.T) {
		got, err := coerceStringSlice("f", []string{"a"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if diff := cmp.Diff([]string{"a"}, got); diff != "" {
			t.Fatalf("mismatch:\n%s", diff)
		}
	})
	t.Run("non-string element errors", func(t *testing.T) {
		if _, err := coerceStringSlice("f", []any{"a", 7}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("wrong type errors", func(t *testing.T) {
		if _, err := coerceStringSlice("f", "abc"); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("nil returns nil", func(t *testing.T) {
		got, err := coerceStringSlice("f", nil)
		if err != nil || got != nil {
			t.Fatalf("got (%v, %v)", got, err)
		}
	})
}

func TestCoerceMap(t *testing.T) {
	t.Run("map passes through", func(t *testing.T) {
		in := map[string]any{"k": "v"}
		got, err := coerceMap("f", in)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if diff := cmp.Diff(in, got); diff != "" {
			t.Fatalf("mismatch:\n%s", diff)
		}
	})
	t.Run("wrong type errors", func(t *testing.T) {
		if _, err := coerceMap("f", "abc"); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestWithoutEmptyMembers(t *testing.T) {
	cases := map[string]struct {
		in, want any
	}{
		"object drops its null, empty-list and empty-object members": {
			in:   map[string]any{"name": "x", "limit": nil, "tags": []any{}, "window": map[string]any{}},
			want: map[string]any{"name": "x"},
		},
		"each object in a list drops its empty members": {
			in:   []any{map[string]any{"toolsetName": "lookup", "configValues": map[string]any{}}, "not-an-object"},
			want: []any{map[string]any{"toolsetName": "lookup"}, "not-an-object"},
		},
		"goes one level deep, so an author's null inside a kept member survives": {
			in:   map[string]any{"configValues": map[string]any{"proxy": nil}},
			want: map[string]any{"configValues": map[string]any{"proxy": nil}},
		},
		"keeps non-empty members and values that are not containers": {
			in:   map[string]any{"tags": []any{"orders"}, "count": 0.0, "enabled": false},
			want: map[string]any{"tags": []any{"orders"}, "count": 0.0, "enabled": false},
		},
		"a scalar passes through": {
			in:   "standard",
			want: "standard",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, withoutEmptyMembers(tc.in)); diff != "" {
				t.Errorf("withoutEmptyMembers (-want +got):\n%s", diff)
			}
		})
	}
}
