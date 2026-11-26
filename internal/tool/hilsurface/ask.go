// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hilsurface

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	gwa2ui "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const PendingKindAsk = "hil_ask"

func AskUserQuestionResult(questions []any, resp *a2a.UserInputResponseData, timeout time.Duration) *tool.Result {
	var resultData map[string]any
	switch resp.Status {
	case "answered":
		resultData = map[string]any{
			"status":  "answered",
			"answers": ExtractAnswers(questions, resp.Data),
		}
	case "cancelled":
		resultData = map[string]any{
			"status":  "cancelled",
			"message": "User cancelled the request.",
		}
	case "timed_out":
		resultData = map[string]any{
			"status":     "timed_out",
			"message":    fmt.Sprintf("User did not respond within %d minutes.", int(timeout.Minutes())),
			"error_code": tool.DispatchErrorCode(taxonomy.DispatchClassUserInputUnanswered),
		}
	default:
		resultData = map[string]any{
			"status":  resp.Status,
			"message": "Unexpected response status.",
		}
	}
	resultJSON, _ := json.Marshal(resultData)
	return tool.OK(string(resultJSON), tool.WithData(resultData))
}

func ExtractAnswers(questions []any, data map[string]any) map[string]string {
	result := make(map[string]string)
	for i, qRaw := range questions {
		q, ok := qRaw.(map[string]any)
		if !ok {
			continue
		}
		questionText, _ := q["question"].(string)
		result[questionText] = gwa2ui.QuestionAnswer(data, i)
	}
	return result
}
