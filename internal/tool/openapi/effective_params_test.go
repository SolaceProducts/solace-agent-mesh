// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func param(in, name string, required bool) *openapi3.ParameterRef {
	return &openapi3.ParameterRef{Value: &openapi3.Parameter{Name: name, In: in, Required: required}}
}

func describeParameters(params openapi3.Parameters) []string {
	out := make([]string, len(params))
	for i, ref := range params {
		if ref == nil || ref.Value == nil {
			out[i] = "<unresolved>"
			continue
		}
		out[i] = ref.Value.In + ":" + ref.Value.Name
		if ref.Value.Required {
			out[i] += "!"
		}
	}
	return out
}

func TestEffectiveParameters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		pathLevel openapi3.Parameters
		operation openapi3.Parameters
		want      []string
	}{
		{
			name: "nothing declared anywhere",
			want: []string{},
		},
		{
			name:      "operation parameters alone",
			operation: openapi3.Parameters{param("query", "limit", false)},
			want:      []string{"query:limit"},
		},
		{
			name:      "path-item parameters alone",
			pathLevel: openapi3.Parameters{param("path", "petId", true)},
			want:      []string{"path:petId!"},
		},
		{
			name:      "the operation's own parameters come before the path item's",
			pathLevel: openapi3.Parameters{param("path", "petId", true)},
			operation: openapi3.Parameters{param("query", "limit", false)},
			want:      []string{"query:limit", "path:petId!"},
		},
		{
			name:      "an operation parameter of the same name and location replaces the path-item one",
			pathLevel: openapi3.Parameters{param("query", "limit", false), param("path", "petId", true)},
			operation: openapi3.Parameters{param("query", "limit", true)},
			want:      []string{"query:limit!", "path:petId!"},
		},
		{
			name:      "the replacement may relax a required flag",
			pathLevel: openapi3.Parameters{param("query", "limit", true)},
			operation: openapi3.Parameters{param("query", "limit", false)},
			want:      []string{"query:limit"},
		},
		{
			name:      "the same name in another location is a different parameter",
			pathLevel: openapi3.Parameters{param("query", "filter", false)},
			operation: openapi3.Parameters{param("header", "filter", true)},
			want:      []string{"header:filter!", "query:filter"},
		},
		{
			name:      "names are case-sensitive outside headers",
			pathLevel: openapi3.Parameters{param("query", "Limit", false)},
			operation: openapi3.Parameters{param("query", "limit", false)},
			want:      []string{"query:limit", "query:Limit"},
		},
		{
			name:      "header names compare case-insensitively",
			pathLevel: openapi3.Parameters{param("header", "X-Request-Id", false)},
			operation: openapi3.Parameters{param("header", "x-request-id", true)},
			want:      []string{"header:x-request-id!"},
		},
		{
			name:      "an unresolved path-item parameter is dropped and cannot hide an operation one",
			pathLevel: openapi3.Parameters{nil, {}, param("query", "limit", false)},
			operation: openapi3.Parameters{param("query", "limit", true)},
			want:      []string{"query:limit!"},
		},
		{
			name:      "an unresolved operation parameter overrides nothing",
			pathLevel: openapi3.Parameters{param("query", "limit", false)},
			operation: openapi3.Parameters{nil, {}},
			want:      []string{"<unresolved>", "<unresolved>", "query:limit"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := effectiveParameters(tc.pathLevel, tc.operation)
			if described := describeParameters(got); !slices.Equal(described, tc.want) {
				t.Fatalf("effectiveParameters = %v, want %v", described, tc.want)
			}
		})
	}
}

func TestEffectiveParameters_LeavesItsInputsUntouched(t *testing.T) {
	t.Parallel()
	pathLevel := openapi3.Parameters{param("query", "limit", false), param("path", "petId", true)}
	operation := openapi3.Parameters{param("query", "limit", true)}

	_ = effectiveParameters(pathLevel, operation)

	if got := describeParameters(pathLevel); !slices.Equal(got, []string{"query:limit", "path:petId!"}) {
		t.Errorf("path-item parameters were modified: %v", got)
	}
	if got := describeParameters(operation); !slices.Equal(got, []string{"query:limit!"}) {
		t.Errorf("operation parameters were modified: %v", got)
	}
}
