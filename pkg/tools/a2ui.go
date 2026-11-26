// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"

	gwa2ui "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func generateA2UISurface(requestID string, args map[string]any) (json.RawMessage, error) {
	return gwa2ui.QuestionSurface(requestID, args)
}

func extractAnswers(questions []any, data map[string]any) map[string]string {
	return hilsurface.ExtractAnswers(questions, data)
}

func renderableOptions(q map[string]any) []map[string]any {
	return gwa2ui.RenderableOptions(q)
}

func coerceToSlice(v any) []any {
	return gwa2ui.CoerceToSlice(v)
}

func questionMaps(questions []any) []map[string]any {
	return gwa2ui.QuestionMaps(questions)
}
