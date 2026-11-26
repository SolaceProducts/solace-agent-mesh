// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"errors"
	"testing"
)

func TestInClause_Strings(t *testing.T) {
	ph, args, err := InClause([]string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ph != "?,?,?" {
		t.Errorf("expected '?,?,?', got %q", ph)
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(args))
	}
	if args[0].(string) != "a" || args[1].(string) != "b" || args[2].(string) != "c" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestInClause_Ints(t *testing.T) {
	ph, args, err := InClause([]int{10, 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ph != "?,?" {
		t.Errorf("expected '?,?', got %q", ph)
	}
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	if args[0].(int) != 10 || args[1].(int) != 20 {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestInClause_Single(t *testing.T) {
	ph, args, err := InClause([]string{"only"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ph != "?" {
		t.Errorf("expected '?', got %q", ph)
	}
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(args))
	}
}

func TestInClause_Empty(t *testing.T) {
	ph, args, err := InClause([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ph != "" {
		t.Errorf("expected empty string, got %q", ph)
	}
	if args != nil {
		t.Errorf("expected nil args, got %v", args)
	}
}

func TestInClause_Cap(t *testing.T) {
	values := make([]int, InClauseCap+1)
	_, _, err := InClause(values)
	if err == nil {
		t.Fatal("expected ErrTooManyValues for slice over cap, got nil")
	}
	if !errors.Is(err, ErrTooManyValues) {
		t.Errorf("error = %v, want errors.Is(err, ErrTooManyValues) = true", err)
	}
}

func TestInClause_AtCap(t *testing.T) {
	values := make([]int, InClauseCap)
	ph, args, err := InClause(values)
	if err != nil {
		t.Fatalf("expected no error at exactly cap, got %v", err)
	}
	if len(args) != InClauseCap {
		t.Errorf("len(args) = %d, want %d", len(args), InClauseCap)
	}
	if ph == "" {
		t.Error("placeholders empty at cap")
	}
}
