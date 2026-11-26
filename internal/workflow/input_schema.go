// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
)

const maxViolationChars = 2000

func (w *WorkflowInstance) initInputSchema() {
	compiled, err := structured.CompileInputSchema(w.cfg.InputSchema)
	if err != nil {
		w.logger.Warn("workflow input_schema does not compile; run input will not be validated", "error", err)
		return
	}
	w.inputSchema = compiled
}

func (w *WorkflowInstance) inputViolations(input map[string]any) []string {
	if w == nil || w.inputSchema == nil {
		return nil
	}
	return w.inputSchema.Validate(input)
}

func (w *WorkflowInstance) textRunInput(text string) map[string]any {
	return structured.TextRunInput(text, w.inputSchema)
}

func requiredInputHint(schema map[string]any) string {
	properties, _ := schema["properties"].(map[string]any)
	required := requiredFieldNames(schema)

	described := make([]string, 0, len(required))
	for _, name := range required {
		described = append(described, describeField(name, properties[name]))
	}
	var optional []string
	for name := range properties {
		if !slices.Contains(required, name) {
			optional = append(optional, name)
		}
	}
	slices.Sort(optional)

	var parts []string
	if len(described) > 0 {
		parts = append(parts, "Required: "+strings.Join(described, ", "))
	}
	if len(optional) > 0 {
		parts = append(parts, "Optional: "+strings.Join(optional, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Send the input as a JSON object. " + strings.Join(parts, ". ") + "."
}

func requiredFieldNames(schema map[string]any) []string {
	var names []string
	switch required := schema["required"].(type) {
	case []any:
		for _, item := range required {
			if name, ok := item.(string); ok {
				names = append(names, name)
			}
		}
	case []string:
		names = append(names, required...)
	}
	return names
}

func describeField(name string, property any) string {
	declared, _ := property.(map[string]any)
	switch fieldType := declared["type"].(type) {
	case string:
		return fmt.Sprintf("%s (%s)", name, fieldType)
	case []any:
		var listed []string
		for _, item := range fieldType {
			if t, ok := item.(string); ok {
				listed = append(listed, t)
			}
		}
		return describeTypes(name, listed)
	case []string:
		return describeTypes(name, fieldType)
	}
	return name
}

func describeTypes(name string, types []string) string {
	nonNull := slices.DeleteFunc(slices.Clone(types), func(t string) bool { return t == "null" })
	if len(nonNull) == 0 {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, strings.Join(nonNull, " or "))
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "... (truncated)"
}

func (e *DAGExecutor) rejectInvalidInput(violations []string) {
	message := structured.InputValidationFailedPrefix + truncateRunes(strings.Join(violations, "; "), maxViolationChars)
	if hint := requiredInputHint(e.cfg.InputSchema); hint != "" {
		message += ". " + hint
	}
	e.endEarly(a2a.TerminationInvalidInput)
	e.state.PendingOutcome = "failure"
	e.state.PendingErrorMsg = message
	e.publishFinalResponse()
}

type peerFailure struct{ err error }

func (f peerFailure) Error() string { return f.err.Error() }
func (f peerFailure) Unwrap() error { return f.err }

func (f peerFailure) LogValue() slog.Value {
	return slog.StringValue(failureLogText(f.err.Error()))
}

func failureLogText(errMsg string) string {
	if strings.Contains(errMsg, structured.InputValidationFailedPrefix) {
		return fmt.Sprintf("input validation failure (%d characters)", utf8.RuneCountInString(errMsg))
	}
	return errMsg
}
