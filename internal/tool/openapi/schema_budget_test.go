// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func sharedSchemaDAGSpec(levels int) string {
	var b strings.Builder
	b.WriteString(`openapi: 3.0.0
info:
  title: Dag
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /dag:
    post:
      operationId: postDag
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/S0"
      responses:
        "200":
          description: ok
components:
  schemas:
`)
	for i := 0; i < levels; i++ {
		fmt.Fprintf(&b, "    S%d:\n      type: object\n      properties:\n        a:\n          $ref: \"#/components/schemas/S%d\"\n        b:\n          $ref: \"#/components/schemas/S%d\"\n", i, i+1, i+1)
	}
	fmt.Fprintf(&b, "    S%d:\n      type: string\n", levels)
	return b.String()
}

func TestDescribeOperations_SharedSchemaDAGIsBoundedAndStubbed(t *testing.T) {
	t.Parallel()

	descriptions, err := DescribeOperations(t.Context(), []byte(sharedSchemaDAGSpec(30)), nil, nil)
	if err != nil {
		t.Fatalf("DescribeOperations: %v", err)
	}
	if len(descriptions) != 1 || !strings.Contains(string(descriptions[0].Parameters), schemaTooLargeDescription) {
		t.Errorf("parameters of an over-budget schema do not carry the %q stub", schemaTooLargeDescription)
	}
}

func TestEnumerateOperations_SharedSchemaDAGIsBounded(t *testing.T) {
	t.Parallel()

	signatures, err := EnumerateOperations(t.Context(), []byte(sharedSchemaDAGSpec(30)), nil, nil)
	if err != nil {
		t.Fatalf("EnumerateOperations: %v", err)
	}
	if len(signatures) != 1 {
		t.Errorf("enumerated %d operations, want 1", len(signatures))
	}
}

func TestDescribeOperations_SharedSchemaDAGUnderTheBudgetRendersInFull(t *testing.T) {
	t.Parallel()

	descriptions, err := DescribeOperations(t.Context(), []byte(sharedSchemaDAGSpec(6)), nil, nil)
	if err != nil {
		t.Fatalf("DescribeOperations: %v", err)
	}
	if strings.Contains(string(descriptions[0].Parameters), schemaTooLargeDescription) {
		t.Errorf("a schema well under the budget was stubbed: %s", descriptions[0].Parameters)
	}
}

func TestDescribeAndEnumerateOperations_ACancelledContextIsAnError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := DescribeOperations(ctx, []byte(operationsSpec), nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("DescribeOperations on a cancelled context: err = %v, want context.Canceled", err)
	}
	if _, err := EnumerateOperations(ctx, []byte(operationsSpec), nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("EnumerateOperations on a cancelled context: err = %v, want context.Canceled", err)
	}
}

func differingSiblingsSchemaSpec(levels int) string {
	var b strings.Builder
	b.WriteString(`openapi: 3.0.0
info:
  title: Siblings
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /tree:
    post:
      operationId: postTree
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/X0"
      responses:
        "200":
          description: ok
components:
  schemas:
`)
	for i := 0; i < levels; i++ {
		fmt.Fprintf(&b, "    X%d:\n      type: object\n      description: x%d\n      properties:\n        a:\n          $ref: \"#/components/schemas/X%d\"\n        b:\n          $ref: \"#/components/schemas/Y%d\"\n", i, i, i+1, i+1)
		fmt.Fprintf(&b, "    Y%d:\n      type: object\n      description: y%d\n      properties:\n        a:\n          $ref: \"#/components/schemas/Y%d\"\n        b:\n          $ref: \"#/components/schemas/X%d\"\n", i, i, i+1, i+1)
	}
	fmt.Fprintf(&b, "    X%d:\n      type: string\n    Y%d:\n      type: string\n", levels, levels)
	return b.String()
}

func TestDescribeOperations_AnOverBudgetSchemaRendersTheSameOnEveryRun(t *testing.T) {
	t.Parallel()

	spec := []byte(differingSiblingsSchemaSpec(30))
	var first string
	for run := range 10 {
		descriptions, err := DescribeOperations(t.Context(), spec, nil, nil)
		if err != nil {
			t.Fatalf("run %d: DescribeOperations: %v", run, err)
		}
		got := string(descriptions[0].Parameters)
		if !strings.Contains(got, schemaTooLargeDescription) {
			t.Fatalf("run %d: the fixture is under the node budget, so it proves nothing", run)
		}
		if run == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d rendered differently from run 0: which properties become stubs once the budget is spent must not depend on map iteration order", run)
		}
	}
}
