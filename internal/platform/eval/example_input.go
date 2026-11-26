// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const maxExampleInputDataBytes = 256 * 1024

const inputDataColumn = "input_data"

var (
	errInputDataNotObject = errors.New("must be a JSON object")
	errInputDataTooLarge  = fmt.Errorf("must be at most %d bytes as JSON", maxExampleInputDataBytes)
)

func isAbsentInputData(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func CanonicalInputData(raw []byte) (json.RawMessage, error) {
	if isAbsentInputData(raw) {
		return nil, nil
	}
	return canonicalInputObject(raw)
}

func canonicalInputObject(raw []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var object map[string]any
	if err := dec.Decode(&object); err != nil || object == nil {
		return nil, errInputDataNotObject
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errInputDataNotObject
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(object); err != nil {
		return nil, errInputDataNotObject
	}
	canonical := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(canonical) > maxExampleInputDataBytes {
		return nil, errInputDataTooLarge
	}
	return canonical, nil
}

func storedInputData(raw []byte) (*string, error) {
	canonical, err := CanonicalInputData(raw)
	if err != nil || canonical == nil {
		return nil, err
	}
	text := string(canonical)
	return &text, nil
}

func encodeInputData(raw json.RawMessage) (*string, error) {
	stored, err := storedInputData(raw)
	if err != nil {
		return nil, &ValidationError{Field: inputDataColumn, Message: "inputData " + err.Error()}
	}
	return stored, nil
}

func inputDataJSON(stored *string) json.RawMessage {
	if stored == nil || !json.Valid([]byte(*stored)) {
		return nil
	}
	return json.RawMessage(*stored)
}

func inputDataSnapshot(stored *string) any {
	if raw := inputDataJSON(stored); raw != nil {
		return raw
	}
	return nil
}

func parseInputDataCell(cell string) (*string, error) {
	if strings.TrimSpace(cell) == "" {
		return nil, nil
	}
	canonical, err := canonicalInputObject([]byte(cell))
	if err != nil {
		return nil, fmt.Errorf("%s %w", inputDataColumn, err)
	}
	text := string(canonical)
	return &text, nil
}

func inputDataOf(row map[string]json.RawMessage) (*string, error) {
	for _, key := range []string{"inputData", inputDataColumn} {
		raw, ok := row[key]
		if !ok || isAbsentInputData(raw) {
			continue
		}
		stored, err := storedInputData(raw)
		if err != nil {
			return nil, fmt.Errorf("%s %w", key, err)
		}
		return stored, nil
	}
	return nil, nil
}

func hasExampleInput(prompt string, hasInputData bool) bool {
	return strings.TrimSpace(prompt) != "" || hasInputData
}

func sameInputData(stored *string, desired *string) bool {
	if stored == nil || desired == nil {
		return stored == nil && desired == nil
	}
	if *stored == *desired {
		return true
	}
	canonical, err := CanonicalInputData([]byte(*stored))
	return err == nil && string(canonical) == *desired
}

func inputDataRefusal(registry *features.Registry) error {
	if workflowEvalsEnabled(registry) {
		return nil
	}
	return &ValidationError{Field: inputDataColumn, Message: workflowEvalsDisabledMessage()}
}

func expectedPathRefusal(registry *features.Registry) error {
	if workflowEvalsEnabled(registry) {
		return nil
	}
	return &ValidationError{Field: expectedPathColumn, Message: workflowEvalsDisabledMessage()}
}

func gatedFieldsRefusal(registry *features.Registry, withInput, withPath, withFacts bool) error {
	gates := []struct {
		carried bool
		refusal func(*features.Registry) error
	}{
		{withInput, inputDataRefusal},
		{withPath, expectedPathRefusal},
		{withFacts, expectedFactsRefusal},
	}
	for _, gate := range gates {
		if !gate.carried {
			continue
		}
		if err := gate.refusal(registry); err != nil {
			return err
		}
	}
	return nil
}

func expectedFactsRefusal(registry *features.Registry) error {
	if workflowEvalsEnabled(registry) {
		return nil
	}
	return &ValidationError{Field: expectedFactsColumn, Message: workflowEvalsDisabledMessage()}
}

const maxListedPromptlessExamples = 10

func isPromptless(ex *store.EvalExample) bool {
	return strings.TrimSpace(ex.Prompt) == ""
}

func refusePromptlessExamples(examples []*store.EvalExample) error {
	var listed []string
	promptless := 0
	for _, ex := range examples {
		if !isPromptless(ex) {
			continue
		}
		promptless++
		if len(listed) < maxListedPromptlessExamples {
			listed = append(listed, strconv.Itoa(ex.SequenceNumber))
		}
	}
	if promptless == 0 {
		return nil
	}
	sequences := strings.Join(listed, ", ")
	if more := promptless - len(listed); more > 0 {
		sequences += fmt.Sprintf(" and %d more", more)
	}
	return &ValidationError{
		Field: "dataset_id",
		Message: fmt.Sprintf("an agent experiment sends each example's prompt, and %d examples of the dataset have none "+
			"(sequence numbers %s); give them a prompt or remove them before triggering a run", promptless, sequences),
	}
}
