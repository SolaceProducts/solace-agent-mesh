// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const planResponseTimeoutSeconds = 600

const planVerificationCapability = "interactive_plan_verification"

func generateResearchPlan(
	ctx context.Context,
	llm llmCallFunc,
	researchQuestion string,
	queries []string,
) []string {
	if len(queries) == 0 {
		return queries
	}
	queriesText := "- " + strings.Join(queries, "\n- ")
	prompt := fmt.Sprintf(`You are a research planning specialist. Convert these search queries into a clear,
human-readable research plan with 3-6 concise steps.

Research Question: %s

Search Queries:
%s

Requirements:
1. Each step should be a clear, actionable description (one sentence)
2. Steps should be in logical order
3. Use plain language, not search query syntax
4. Cover the key aspects of the research
5. Include a final synthesis/compilation step

Respond in JSON format:
{
  "steps": ["step1 description", "step2 description", ...]
}`, researchQuestion, queriesText)

	resp, err := llm(ctx, "", prompt, 0.5)
	if err != nil {
		return queries
	}

	var parsed struct {
		Steps []string `json:"steps"`
	}
	if perr := parseJSONFromLLMResponse(resp, &parsed, "steps"); perr != nil || len(parsed.Steps) == 0 {
		return queries
	}
	if len(parsed.Steps) > 6 {
		parsed.Steps = parsed.Steps[:6]
	}
	return parsed.Steps
}

func regenerateQueriesFromSteps(
	ctx context.Context,
	llm llmCallFunc,
	steps []string,
	researchQuestion string,
) []string {
	if len(steps) == 0 {
		return steps
	}
	stepsText := "- " + strings.Join(steps, "\n- ")
	prompt := fmt.Sprintf(`Convert these research plan steps into effective search engine queries.

Research Question: %s

Plan Steps:
%s

Generate one search query per step, optimized for web search engines.

Respond in JSON format:
{
  "queries": ["query1", "query2", ...]
}`, researchQuestion, stepsText)

	resp, err := llm(ctx, "", prompt, 0.5)
	if err != nil {
		return steps
	}

	var parsed struct {
		Queries []string `json:"queries"`
	}
	if perr := parseJSONFromLLMResponse(resp, &parsed, "queries"); perr != nil || len(parsed.Queries) == 0 {
		return steps
	}
	if len(parsed.Queries) > len(steps) {
		parsed.Queries = parsed.Queries[:len(steps)]
	}
	return parsed.Queries
}

func isInteractivePlanClient(toolCtx *tool.Context, toolConfig map[string]any) bool {
	if toolCtx == nil {
		return false
	}
	if v, ok := toolCtx.GatewayCapabilities[planVerificationCapability]; ok {
		return v
	}

	allowlistAny, ok := toolConfig["interactive_plan_verification_clients"]
	if !ok {
		return false
	}
	var allowlist []string
	switch v := allowlistAny.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				allowlist = append(allowlist, s)
			}
		}
	case []string:
		allowlist = v
	}
	if len(allowlist) == 0 {
		return false
	}
	if slices.Contains(allowlist, "*") {
		return true
	}
	if toolCtx.ClientID == "" {
		return false
	}
	return slices.Contains(allowlist, toolCtx.ClientID)
}

type verificationOutcome struct {
	Cancelled        bool
	CancelledTimeout bool
	Steps            []string
	Reason           string
}

func runPlanVerification(
	ctx context.Context,
	toolCtx *tool.Context,
	planID, title, researchQuestion, researchType string,
	steps, sources []string,
	maxIterations, maxRuntimeSeconds int,
) verificationOutcome {
	logger := slog.Default().With("logger_name", "deep_research_verify", "planID", planID)

	if toolCtx.SendSignal != nil {
		_ = toolCtx.SendSignal(a2a.DeepResearchPlanData{
			Type:              string(a2a.SignalDeepResearchPlan),
			PlanID:            planID,
			AgentName:         toolCtx.AgentName,
			Title:             title,
			ResearchQuestion:  researchQuestion,
			Steps:             steps,
			ResearchType:      researchType,
			MaxIterations:     maxIterations,
			MaxRuntimeSeconds: maxRuntimeSeconds,
			Sources:           ifNilEmpty(sources),
		})
	}

	if toolCtx.AwaitDeepResearchPlanResponse == nil || toolCtx.UserID == "" {
		logger.Info("plan-verification awaiter not wired or user missing; auto-approving")
		return verificationOutcome{Steps: steps}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, planResponseTimeoutSeconds*time.Second)
	defer cancel()

	resp, err := toolCtx.AwaitDeepResearchPlanResponse(timeoutCtx, planID, toolCtx.UserID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger.Warn("plan-verification timed out, publishing stale signal")
			if toolCtx.SendSignal != nil {
				_ = toolCtx.SendSignal(a2a.DeepResearchPlanStaleData{
					Type:   string(a2a.SignalDeepResearchPlanStale),
					PlanID: planID,
					Reason: "timed_out",
				})
			}
			return verificationOutcome{
				Cancelled:        true,
				CancelledTimeout: true,
				Reason:           "timeout",
			}
		}
		logger.Info("plan-verification awaiter ended without response; auto-approving", "error", err)
		return verificationOutcome{Steps: steps}
	}

	if resp == nil {
		logger.Warn("plan-verification returned nil response; auto-approving")
		return verificationOutcome{Steps: steps}
	}
	if resp.Action != "start" {
		logger.Warn("unexpected plan-verification response action", "action", resp.Action)
		return verificationOutcome{Steps: steps}
	}
	if len(resp.Steps) == 0 {
		return verificationOutcome{Steps: steps}
	}
	return verificationOutcome{Steps: resp.Steps}
}

func ifNilEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func newPlanID() string {
	return idgen.New()
}
