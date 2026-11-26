// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func Validate(data any, schema map[string]any) []string {
	compiled, err := compileSchema(schema)
	if err != nil {
		return []string{fmt.Sprintf("schema compilation error: %v", err)}
	}
	return validateWithCompiled(data, compiled)
}

const InputValidationFailedPrefix = taxonomy.StructuredInputInvalidPrefix

type Schema struct {
	compiled   *jsonschema.Schema
	properties map[string]any
}

func Compile(schema map[string]any) (*Schema, error) {
	compiled, err := compileSchema(schema)
	if err != nil {
		return nil, err
	}
	properties, _ := schema["properties"].(map[string]any)
	return &Schema{compiled: compiled, properties: properties}, nil
}

func (s *Schema) Validate(data any) []string {
	return validateWithCompiled(data, s.compiled)
}

func CompileSchema(schema map[string]any) error {
	_, err := Compile(schema)
	return err
}

const schemaBaseURL = "https://sam.invalid/schema.json"

func newCompilerThatRefusesExternalRefs() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.UseLoader(jsonschema.SchemeURLLoader{})
	return c
}

func compileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}

	var schemaDoc any
	if err = json.Unmarshal(schemaBytes, &schemaDoc); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}

	c := newCompilerThatRefusesExternalRefs()
	if err = c.AddResource(schemaBaseURL, schemaDoc); err != nil {
		return nil, fmt.Errorf("add resource: %w", err)
	}

	compiled, err := c.Compile(schemaBaseURL)
	if err != nil {
		if _, external := errors.AsType[*jsonschema.LoadURLError](err); external {
			return nil, fmt.Errorf("compile (external references are not supported, schemas must be self-contained): %w", err)
		}
		return nil, fmt.Errorf("compile: %w", err)
	}
	return compiled, nil
}

func validateWithCompiled(data any, compiled *jsonschema.Schema) []string {
	err := compiled.Validate(data)
	if err == nil {
		return nil
	}

	valErr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{err.Error()}
	}

	return collectErrors(valErr)
}

func collectErrors(ve *jsonschema.ValidationError) []string {
	if len(ve.Causes) > 0 {
		var errs []string
		for _, cause := range ve.Causes {
			errs = append(errs, collectErrors(cause)...)
		}
		return errs
	}

	path := "root"
	if len(ve.InstanceLocation) > 0 {
		path = strings.Join(ve.InstanceLocation, ".")
	}
	return []string{fmt.Sprintf("Validation error at '%s': %s", path, ve.Error())}
}
