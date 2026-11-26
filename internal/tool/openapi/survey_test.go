// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"slices"
	"testing"
)

const disagreeingMediaTypesSpec = `openapi: 3.0.0
info:
  title: Labels
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /labels:
    post:
      operationId: createLabel
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                recipient:
                  type: string
          application/xml:
            schema:
              type: object
              properties:
                address:
                  type: string
      responses:
        "201":
          description: created
  /ping:
    get:
      operationId: ping
      responses:
        "200":
          description: ok
`

func TestSurveyOperations_AgreesWithTheFunctionsItReplaces(t *testing.T) {
	t.Parallel()

	for name, spec := range map[string]string{"shipping": operationsSpec, "disagreeing media types": disagreeingMediaTypesSpec} {
		t.Run(name, func(t *testing.T) {
			survey, err := SurveyOperations(t.Context(), []byte(spec), nil, nil)
			if err != nil {
				t.Fatalf("SurveyOperations: %v", err)
			}
			described, err := DescribeOperations(t.Context(), []byte(spec), nil, nil)
			if err != nil {
				t.Fatalf("DescribeOperations: %v", err)
			}
			names, err := EnumerateToolNames(t.Context(), []byte(spec), nil, nil)
			if err != nil {
				t.Fatalf("EnumerateToolNames: %v", err)
			}
			if got, want := toolNames(survey.Operations), toolNames(described); !slices.Equal(got, want) {
				t.Errorf("surveyed operations %v, DescribeOperations %v", got, want)
			}
			if !slices.Equal(survey.ToolNames, names) {
				t.Errorf("surveyed tool names %v, EnumerateToolNames %v", survey.ToolNames, names)
			}
		})
	}
}

func TestSurveyOperations_NamesTheOperationsWhoseMediaTypesDisagree(t *testing.T) {
	t.Parallel()

	survey, err := SurveyOperations(t.Context(), []byte(disagreeingMediaTypesSpec), nil, nil)
	if err != nil {
		t.Fatalf("SurveyOperations: %v", err)
	}
	if want := []string{"create_label"}; !slices.Equal(survey.MediaTypesDisagree, want) {
		t.Errorf("MediaTypesDisagree = %v, want %v", survey.MediaTypesDisagree, want)
	}
	if want := []string{"create_label", "ping"}; !slices.Equal(survey.ToolNames, want) {
		t.Errorf("ToolNames = %v, want %v: an operation the spec does not settle still has a name", survey.ToolNames, want)
	}
	if want := []string{"ping"}; !slices.Equal(toolNames(survey.Operations), want) {
		t.Errorf("Operations = %v, want only %v", toolNames(survey.Operations), want)
	}
}

func TestValidateOperationIDUniqueness_FollowsTheAllowList(t *testing.T) {
	t.Parallel()

	if err := ValidateOperationIDUniqueness([]byte(clashingOperationsSpec), nil, nil); err == nil {
		t.Error("two operationIds that snake-case to one name passed with no allow_list")
	}
	if err := ValidateOperationIDUniqueness([]byte(clashingOperationsSpec), []string{"other"}, nil); err != nil {
		t.Errorf("an allow_list naming only non-clashing operations failed: %v", err)
	}
}
