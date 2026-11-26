// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type valuesShape struct {
	name     string
	json     string
	notAnObj bool
}

func valuesShapes() []valuesShape {
	return []valuesShape{
		{name: "absent", json: ""},
		{name: "null", json: "null"},
		{name: "empty object", json: "{}"},
		{name: "a string", json: `"hostname=db"`, notAnObj: true},
		{name: "a list", json: `["hostname"]`, notAnObj: true},
	}
}

func artifactWithValuesJSON(name string, f connectorFixture, valuesJSON string) BuildComponentInput {
	content := fmt.Sprintf(`{"name":%q,"description":"A connector the shape tests author.","type":%q,"subtype":%q`, name, f.connType, f.subtype)
	if valuesJSON != "" {
		content += `,"values":` + valuesJSON
	}
	return BuildComponentInput{Kind: kindConnector, Name: name, Content: content + "}"}
}

func TestApply_ConnectorValuesShapes(t *testing.T) {
	t.Parallel()
	for _, f := range connectorFixtures() {
		for _, shape := range valuesShapes() {
			for _, action := range []string{actionCreate, actionUpdate} {
				t.Run(fmt.Sprintf("%s/%s/%s/%s", f.connType, f.subtype, shape.name, action), func(t *testing.T) {
					t.Parallel()
					svc := serviceFor(t, f, true)
					const connectorName = "shape_matrix"
					if action == actionUpdate {
						if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
							Components: []BuildComponentInput{connectorArtifactFor(connectorName, f, f.values())},
						}, testActor); err != nil {
							t.Fatalf("seed: %v", err)
						}
					}
					dry, applied := applyBoth(t, svc, artifactWithValuesJSON(connectorName, f, shape.json))
					dryOp, appliedOp := opFor(t, dry, kindConnector, connectorName), opFor(t, applied, kindConnector, connectorName)

					if shape.notAnObj {
						requireSameFailure(t, dryOp, appliedOp, "values must be a JSON object")
						return
					}
					if shape.json == "{}" && action == actionCreate && f.rejectsEmptyObject != "" {
						requireSameFailure(t, dryOp, appliedOp, f.rejectsEmptyObject)
						return
					}
					for _, op := range []BuildOperationResult{dryOp, appliedOp} {
						if op.Status == statusFailed {
							t.Fatalf("op = %+v, error = %+v, want it accepted", op, op.Error)
						}
					}
					if action == actionUpdate {
						if warnings := strings.Join(appliedOp.Warnings, "\n"); strings.Contains(warnings, "which the") {
							t.Errorf("an update that is silent about values was warned of missing fields: %q", warnings)
						}
						if shape.json != "{}" {
							stored := storedValues(t, svc, connectorName)
							for key := range f.values() {
								if _, ok := stored[key]; !ok {
									t.Errorf("an update silent about values lost the stored %q: %v", key, stored)
								}
							}
						}
						return
					}
					for _, field := range f.required {
						for _, warnings := range [][]string{dryOp.Warnings, appliedOp.Warnings} {
							if !strings.Contains(strings.Join(warnings, "\n"), field) {
								t.Errorf("warnings = %q, want the required field %q named", warnings, field)
							}
						}
					}
				})
			}
		}
	}
}

func TestMergeValidationErrors(t *testing.T) {
	t.Parallel()
	valueErr := &entplatform.ValidationError{Message: `field "port" must be at most 65535`}
	fieldErr := &entplatform.ValidationError{Message: "type is wrong", Field: "type"}
	keyErr := &entplatform.ValidationError{Message: "keys", Field: "values", Details: map[string][]string{"values.host": {"undeclared"}}}
	other := errors.New("database is down")

	if got := mergeValidationErrors(nil, nil); got != nil {
		t.Errorf("no errors merged to %v, want nil", got)
	}
	if got := mergeValidationErrors(nil, valueErr); got != error(valueErr) {
		t.Errorf("a single error was not returned as it is: %v", got)
	}
	for _, order := range [][]error{{other, valueErr}, {valueErr, other}, {nil, other, keyErr}} {
		if got := mergeValidationErrors(order...); got != other {
			t.Errorf("merge(%v) = %v, want the non-validation error returned as it is", order, got)
		}
	}

	t.Run("two errors, the first without details", func(t *testing.T) {
		ve := mergeValidationErrors(valueErr, keyErr).(*entplatform.ValidationError)
		if ve.Message != valueErr.Message+"; keys" || ve.Field != "" {
			t.Errorf("message %q, field %q, want both messages and no single field", ve.Message, ve.Field)
		}
		want := map[string][]string{"values": {valueErr.Message}, "values.host": {"undeclared"}}
		if !reflect.DeepEqual(ve.Details, want) {
			t.Errorf("details = %v, want every message accounted for: %v", ve.Details, want)
		}
	})
	t.Run("the first error's field names its entry", func(t *testing.T) {
		ve := mergeValidationErrors(fieldErr, valueErr).(*entplatform.ValidationError)
		want := map[string][]string{"type": {fieldErr.Message}, "values": {valueErr.Message}}
		if !reflect.DeepEqual(ve.Details, want) || ve.Field != "" {
			t.Errorf("details = %v, field = %q, want %v and no single field", ve.Details, ve.Field, want)
		}
		if !strings.HasPrefix(ve.Message, fieldErr.Message) {
			t.Errorf("message = %q, want the arguments' order kept", ve.Message)
		}
	})
	t.Run("a later error's own field names its entry", func(t *testing.T) {
		ve := mergeValidationErrors(keyErr, fieldErr).(*entplatform.ValidationError)
		if got := ve.Details["type"]; len(got) != 1 || got[0] != fieldErr.Message {
			t.Errorf("details = %v, want the later error entered under its field", ve.Details)
		}
	})
	t.Run("merging does not write into the errors it was given", func(t *testing.T) {
		backing := make([]string, 1, 4)
		backing[0] = "first"
		first := &entplatform.ValidationError{Message: "a", Details: map[string][]string{"values.host": backing}}
		second := &entplatform.ValidationError{Message: "b", Details: map[string][]string{"values.host": {"second"}}}
		merged := mergeValidationErrors(first, second).(*entplatform.ValidationError)
		if len(merged.Details["values.host"]) != 2 {
			t.Fatalf("details = %v, want both messages", merged.Details)
		}
		if got := backing[:2][1]; got != "" {
			t.Errorf("the merge wrote %q into the first error's backing array", got)
		}
	})
}

func mcpRemoteFixture(t *testing.T) connectorFixture {
	t.Helper()
	for _, f := range connectorFixtures() {
		if f.connType == "mcp" {
			return f
		}
	}
	t.Fatal("no mcp fixture")
	return connectorFixture{}
}

func TestApply_ConnectorWarningsFollowTheRuntimeDefaults(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		values       map[string]any
		wantWarned   []string
		wantUnwarned []string
	}{
		{
			name:         "only a server_url",
			values:       map[string]any{"server_url": "https://mcp.example.com/mcp"},
			wantUnwarned: []string{"connection_type", "auth_type", "server_url"},
		},
		{
			name:         "http auth without a scheme",
			values:       map[string]any{"server_url": "https://mcp.example.com/mcp", "auth_type": "http", "auth_http_basic_username": "svc", "auth_http_basic_password": "real-password"},
			wantWarned:   []string{"auth_http_scheme"},
			wantUnwarned: []string{"connection_type", "server_url"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := mcpRemoteFixture(t)
			svc := serviceFor(t, f, true)
			const name = "mcp_defaults"
			dry, applied := applyBoth(t, svc, connectorArtifactFor(name, f, tc.values))
			for _, op := range []BuildOperationResult{opFor(t, dry, kindConnector, name), opFor(t, applied, kindConnector, name)} {
				if op.Status == statusFailed {
					t.Fatalf("op = %+v, error = %+v, want it accepted", op, op.Error)
				}
				warnings := strings.Join(op.Warnings, "\n")
				for _, field := range tc.wantWarned {
					if !strings.Contains(warnings, field) {
						t.Errorf("warnings = %q, want %q named", warnings, field)
					}
				}
				for _, field := range tc.wantUnwarned {
					if strings.Contains(warnings, field) {
						t.Errorf("warnings = %q, want %q not named", warnings, field)
					}
				}
			}
			stored := storedValues(t, svc, name)
			if _, filled := stored["connection_type"]; filled {
				t.Errorf("stored values = %v, want the runtime defaults not written into them", stored)
			}
			if len(stored) != len(tc.values) {
				t.Errorf("stored %d values, authored %d: %v", len(stored), len(tc.values), stored)
			}
		})
	}
}
