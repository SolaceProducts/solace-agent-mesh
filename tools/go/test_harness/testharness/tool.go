// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool(
			"generate_test_plan",
			"Generate a structured test plan (sample input + expected output) for the workflow or agent the test harness wraps.",
			generateTestPlan,
			sdk.WithInstructions(generateTestPlanInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
		sdk.NewTool(
			"generate_recommendations",
			"Given a test plan and the actual output the wrapped component produced, return a prioritized list of specific change recommendations the build agent can apply.",
			generateRecommendations,
			sdk.WithInstructions(generateRecommendationsInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}
