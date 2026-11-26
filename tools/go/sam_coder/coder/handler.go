// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"fmt"
	"math"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func runData(o *Outcome) map[string]any {
	data := outcomeData(o)
	if o.Changes != nil {
		data["diff"] = o.Changes.Diff
		data["files_changed"] = o.Changes.Files
		if o.Changes.Truncated {
			data["diff_truncated"] = true
		}
	}
	if o.DiffErr != "" {
		data["diff_error"] = o.DiffErr
	}
	if o.Plan != "" {
		data["plan"] = o.Plan
	}
	if o.PlanCheck != nil {
		data["plan_check"] = *o.PlanCheck
	}
	if o.Verify != nil {
		data["verify"] = *o.Verify
		data["verify_rounds"] = o.VerifyRounds
	}
	return data
}

func runResult(o *Outcome, data map[string]any, wantsResult bool) *sdk.Result {
	msg := summaryMessage(o)
	switch {
	case wantsResult && o.Result == nil:
		return sdk.Partial(noResultMessage(msg), sdk.WithData(data))
	case o.Answered():
		return sdk.OK(msg, sdk.WithData(data))
	default:
		return sdk.Partial(msg, sdk.WithData(data))
	}
}

func noResultMessage(msg string) string {
	return msg + "\nno result: the model did not call submit_result with an answer matching output_schema"
}

func outcomeData(o *Outcome) map[string]any {
	data := map[string]any{
		"stop_reason": o.StopReason,
		"report":      o.Report,
		"steps":       o.Steps,
		"tool_calls":  o.ToolCalls,
		"tools_used":  o.ToolsUsed,
		"model":       o.Model,
		"usage":       o.Usage,
		"timing":      timingData(o),
	}
	if o.Err != "" {
		data["error"] = o.Err
	}
	if o.Result != nil {
		data["result"] = o.Result
	}
	return data
}

func timingData(o *Outcome) map[string]any {
	tools := make(map[string]float64, len(o.ToolTime))
	for name, d := range o.ToolTime {
		tools[name] = math.Round(d.Seconds()*10) / 10
	}
	return map[string]any{
		"model_seconds": math.Round(o.ModelTime.Seconds()*10) / 10,
		"tool_seconds":  tools,
	}
}

func summaryMessage(o *Outcome) string {
	var b strings.Builder
	if o.Report != "" {
		b.WriteString(o.Report)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "stop_reason=%s steps=%d tool_calls=%d tokens=%d", o.StopReason, o.Steps, o.ToolCalls, o.Usage.TotalTokens)
	switch {
	case o.Changes == nil:
	case o.Changes.Stat != "":
		fmt.Fprintf(&b, "\nchanges: %s", o.Changes.Stat)
	default:
		b.WriteString("\nchanges: none")
	}
	if o.Verify != nil {
		state := fmt.Sprintf("exit %d", o.Verify.ExitCode)
		if o.Verify.TimedOut {
			state = "timed out"
		}
		fmt.Fprintf(&b, "\nverify (%s): %s", o.Verify.Command, state)
	}
	if o.Err != "" {
		fmt.Fprintf(&b, "\nerror: %s", o.Err)
	}
	return b.String()
}
