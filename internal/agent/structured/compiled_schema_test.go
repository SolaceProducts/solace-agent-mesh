// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"strings"
	"sync"
	"testing"
)

func tripSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"destination": map[string]any{"type": "string"},
			"needsHotel":  map[string]any{"type": "boolean"},
		},
		"required": []any{"destination", "needsHotel"},
	}
}

func TestCompiledSchema_ReportsEveryViolationAndMatchesValidate(t *testing.T) {
	schema := tripSchema()
	compiled, err := Compile(schema)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	for _, data := range []any{
		map[string]any{"destination": "Porto", "needsHotel": true},
		map[string]any{"needsHotel": "banana"},
		map[string]any{"text": "hi"},
	} {
		got, want := compiled.Validate(data), Validate(data, schema)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("Validate(%v): compiled = %v, one-shot = %v", data, got, want)
		}
	}

	joined := strings.Join(compiled.Validate(map[string]any{"needsHotel": "banana"}), "\n")
	for _, field := range []string{"destination", "needsHotel"} {
		if !strings.Contains(joined, field) {
			t.Errorf("violations do not name %q:\n%s", field, joined)
		}
	}
}

func TestCompile_RejectsMalformedSchema(t *testing.T) {
	if _, err := Compile(map[string]any{"type": "strng"}); err == nil {
		t.Fatal("Compile accepted a schema with an unknown type")
	}
	if err := CompileSchema(map[string]any{"type": "strng"}); err == nil {
		t.Fatal("CompileSchema accepted a schema with an unknown type")
	}
}

func TestCompiledSchema_ConcurrentValidate(t *testing.T) {
	compiled, err := Compile(tripSchema())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			valid := map[string]any{"destination": "Porto", "needsHotel": true}
			invalid := map[string]any{"needsHotel": "banana"}
			for range 50 {
				if got := compiled.Validate(valid); len(got) != 0 {
					t.Errorf("goroutine %d: valid input reported %v", i, got)
					return
				}
				if got := compiled.Validate(invalid); len(got) == 0 {
					t.Errorf("goroutine %d: invalid input reported no violations", i)
					return
				}
			}
		})
	}
	wg.Wait()
}
