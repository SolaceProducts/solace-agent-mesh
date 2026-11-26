// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	submitResultTool = "submit_result"
	outputSchemaURL  = "https://sam-coder.invalid/output_schema.json"
	resultNudges     = 1
)

const submitResultDescription = "Submit your final answer. Its arguments must match the caller's schema; a mismatch is returned as an error to fix. Call it once, when you are done, instead of replying with text."

const resultPrompt = `
When you are done, call submit_result with your answer instead of replying with text. Its arguments must match its schema exactly.
`

const resultNudgePrompt = `You replied without calling submit_result. Call submit_result now with your answer; its arguments must match its schema.`

const maxStepsResultPrompt = `You have reached the step limit for this task. Call submit_result now with your answer so far; note in it anything left undone if the schema has room.`

type resultSpec struct {
	tool     sdk.ChatTool
	schema   *jsonschema.Schema
	validate func(json.RawMessage) error
}

func newResultSpec(raw map[string]any) (*resultSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if t, ok := raw["type"]; ok && t != "object" {
		return nil, fmt.Errorf("output_schema must describe an object, got type %v", t)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("output_schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("output_schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err = c.AddResource(outputSchemaURL, doc); err != nil {
		return nil, fmt.Errorf("output_schema: %w", err)
	}
	schema, err := c.Compile(outputSchemaURL)
	if err != nil {
		return nil, fmt.Errorf("output_schema is not a valid JSON Schema: %w", err)
	}
	params := make(map[string]any, len(raw)+1)
	for k, v := range raw {
		params[k] = v
	}
	params["type"] = "object"
	return &resultSpec{
		tool:   sdk.ChatTool{Name: submitResultTool, Description: submitResultDescription, Parameters: params},
		schema: schema,
	}, nil
}

func (r *resultSpec) callerCheck(arguments string) string {
	if r.validate == nil {
		return ""
	}
	if err := r.validate(json.RawMessage(arguments)); err != nil {
		return "error: submit_result failed the caller's checks; fix these and call it again:\n" + err.Error()
	}
	return ""
}

func (r *resultSpec) check(arguments string) (any, string) {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(arguments))
	if err != nil {
		return nil, "error: submit_result arguments are not valid JSON: " + err.Error()
	}
	if err := r.schema.Validate(inst); err != nil {
		var verr *jsonschema.ValidationError
		if errors.As(err, &verr) {
			return nil, "error: submit_result does not match the schema; fix these and call it again:\n" + verr.Error()
		}
		return nil, "error: submit_result does not match the schema: " + err.Error()
	}
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err != nil {
		return nil, "error: submit_result arguments are not valid JSON: " + err.Error()
	}
	return value, ""
}
