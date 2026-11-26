// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package idgen

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNew_ReturnsValidUUIDv7(t *testing.T) {
	id := New()
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("invalid UUID: %v", err)
	}
	if parsed.Version() != 7 {
		t.Errorf("expected UUIDv7, got v%d: %s", parsed.Version(), id)
	}
}

func TestNew_Unique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := range 1000 {
		id := New()
		if seen[id] {
			t.Fatalf("duplicate UUID at iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}

func TestNew_TimeOrdered(t *testing.T) {
	a := New()
	b := New()
	if a >= b {
		t.Errorf("expected %s < %s (time-ordered)", a, b)
	}
}

func TestIsCanonicalAcceptsOnlyTheStoredSpelling(t *testing.T) {
	id := New()
	for s, want := range map[string]bool{
		id:                              true,
		strings.ToUpper(id):             false,
		"{" + id + "}":                  false,
		"urn:uuid:" + id:                false,
		strings.ReplaceAll(id, "-", ""): false,
		"alice@example.com":             false,
		"":                              false,
	} {
		if got := IsCanonical(s); got != want {
			t.Errorf("IsCanonical(%q) = %t, want %t", s, got, want)
		}
	}
}

func TestIsCanonical_RejectsTheNilUUID(t *testing.T) {
	if IsCanonical("00000000-0000-0000-0000-000000000000") {
		t.Error("the nil UUID is canonical; a zero-value id would be booked as a real row")
	}
	if !IsCanonical(New()) {
		t.Error("a minted id is not canonical")
	}
}
