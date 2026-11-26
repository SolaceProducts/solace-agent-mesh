// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"slices"
	"strings"
	"testing"
)

type question struct {
	ask     string
	surface string
	events  []EventName
	fields  map[EventName][]string
}

var questions = []question{
	{
		ask:     "What % of installs reach each milestone: LLM configured, first agent saved, first successful run, first chat response, and where is the biggest drop-off?",
		surface: SurfaceServer,
		events:  []EventName{EventModelConfigured, EventAgentCreated, EventAgentRunCompleted},
		fields: map[EventName][]string{
			EventAgentCreated:      {PropSubmittedFrom},
			EventModelConfigured:   {PropSubmittedFrom},
			EventAgentRunCompleted: {PropOutcome},
		},
	},
	{
		ask:     "The chat-response milestone, which only the browser can see.",
		surface: SurfaceUI,
		events:  []EventName{EventChatFirstResponse},
	},
	{
		ask:     "What is the median and p90 time from first launch to first successful agent run?",
		surface: SurfaceUI,
		events:  []EventName{EventAppLaunched},
	},
	{
		ask:     "What % of first launches engage with the built-in getting-started suggestions on the chat welcome screen, and which are used most?",
		surface: SurfaceUI,
		events:  []EventName{EventAppLaunched, EventChatSuggestionUsed},
		fields: map[EventName][]string{
			EventChatSuggestionUsed: {PropSuggestionLabel},
		},
	},
	{
		ask:     "Which step loses the most people: model configuration, agent creation started, validation failure, save, or deployment?",
		surface: SurfaceUI,
		events:  []EventName{EventAgentCreationStart, EventFormValidationError},
		fields: map[EventName][]string{
			EventFormValidationError: {"form", "field", "errorType"},
		},
	},
	{
		ask:     "The deployment step of the same funnel.",
		surface: SurfaceServer,
		events:  []EventName{EventAgentDeployed},
	},

	{
		ask:     "Of the installs that launch, what % click Sign In, sign in, see the terms, and agree to them by choosing a setup, and where is the biggest drop-off?",
		surface: SurfaceUI,
		events: []EventName{
			EventAppLaunched, EventActivationSignInClicked, EventActivationSignedIn,
			EventActivationTermsShown, EventActivationSetupChosen,
		},
		fields: map[EventName][]string{
			EventActivationSetupChosen: {PropSetupPath},
		},
	},
	{
		ask:     "Why do sign-ins, the terms and the acceptance fail?",
		surface: SurfaceUI,
		events:  []EventName{EventActivationSignInFailed, EventActivationTermsError, EventActivationAcceptFailed},
		fields: map[EventName][]string{
			EventActivationSignInFailed: {PropSignInFailure},
		},
	},
	{
		ask:     "How many people accept on the free model, and how many on their own model? Read per person, on the account's side.",
		surface: SurfaceUI,
		events:  []EventName{EventActivationAccepted, EventActivationReplaceConfirmed},
		fields: map[EventName][]string{
			EventActivationAccepted: {PropSetupPath},
		},
	},
	{
		ask:     "How many people run low on or out of free credits, and how many sign in again when the free model's sign-in expires?",
		surface: SurfaceUI,
		events:  []EventName{EventFreeModelCreditsLow, EventFreeModelCreditsOut, EventFreeModelSignInAgain},
	},

	{
		ask:     "What % of installs return within 1, 7, or 30 days of first launch, and does retention differ for installs that achieved a first successful run?",
		surface: SurfaceUI,
		events:  []EventName{EventAppLaunched},
	},

	{
		ask:     "What % of active installs use Agent Builder, and does build complete get reached?",
		surface: SurfaceUI,
		events:  []EventName{EventBuilderOpened, EventBuilderCompleted},
	},
	{
		ask:     "Which built-in tool categories and connector categories are adopted in the first 30 days, and which go untried?",
		surface: SurfaceServer,
		events:  []EventName{EventToolUsed, EventInstallInventory},
		fields: map[EventName][]string{
			EventToolUsed:         {PropCategory},
			EventInstallInventory: {PropConnectorCategories},
		},
	},
	{
		ask:     "What % of active agents and workflows use custom toolsets?",
		surface: SurfaceServer,
		events:  []EventName{EventInstallInventory},
		fields: map[EventName][]string{
			EventInstallInventory: {
				PropAgentCount, PropWorkflowCount,
				PropAgentsWithCustomToolsets, PropWorkflowsWithCustomToolsets,
				PropDeployedAgentCount, PropDeployedWorkflowCount,
				PropDeployedAgentsWithCustomToolsets, PropDeployedWorkflowsWithCustomToolsets,
			},
		},
	},
	{
		ask:     "What % of installs ever add an entrypoint beyond the default, and which types?",
		surface: SurfaceServer,
		events:  []EventName{EventGatewayCreated, EventInstallInventory},
		fields: map[EventName][]string{
			EventGatewayCreated:   {PropGatewayType},
			EventInstallInventory: {PropEntrypointCount, PropEntrypointTypes},
		},
	},
	{
		ask:     "What % of installs invoke peer-agent delegation?",
		surface: SurfaceServer,
		events:  []EventName{EventAgentPeerDelegated},
	},
	{
		ask:     "What % of active installs create a workflow? What % create an eval?",
		surface: SurfaceServer,
		events:  []EventName{EventWorkflowCreated, EventEvalCreated, EventEvalRun},
		fields: map[EventName][]string{
			EventEvalRun: {PropOutcome},
		},
	},
	{
		ask:     "What share of installs that use evals grade with a decision-model evaluator rather than an LLM-as-a-Judge evaluator, which question types do they ask, and how many escalate?",
		surface: SurfaceServer,
		events:  []EventName{EventEvaluatorCreated},
		fields: map[EventName][]string{
			EventEvaluatorCreated: {PropEvaluatorType, PropQuestionType, PropEscalation, PropSubmittedFrom},
		},
	},
	{
		ask:     "Of the cases decision-model evaluators score, what share does the decision model decide, what share escalates to the LLM judge and why, and what share fails?",
		surface: SurfaceServer,
		events:  []EventName{EventEvalRun},
		fields: map[EventName][]string{
			EventEvalRun: {
				PropDecisionEvaluatorCount,
				PropDecisionCasesDecided, PropDecisionCasesEscalated, PropDecisionCasesErrored,
				PropDecisionEscalatedLowConfidence, PropDecisionEscalatedNoConfidence,
				PropDecisionEscalatedProviderError, PropDecisionEscalatedModelLookup,
			},
		},
	},
	{
		ask:     "What share of installs that run evals evaluate workflows rather than agents, and how do those runs end?",
		surface: SurfaceServer,
		events:  []EventName{EventEvalCreated, EventEvalRun},
		fields: map[EventName][]string{
			EventEvalCreated: {PropTargetType},
			EventEvalRun:     {PropTargetType, PropOutcome},
		},
	},
	{
		ask:     "Which evaluator types do eval runs use, how many give each example several trials to measure consistency, and why do workflow trials end early?",
		surface: SurfaceServer,
		events:  []EventName{EventEvalRun},
		fields: map[EventName][]string{
			EventEvalRun: {PropEvaluatorTypes, PropTrialsPerExample, PropWorkflowTrialFailureKinds, PropTargetType},
		},
	},
	{
		ask:     "Which decision-model providers are installs registering, for decision nodes and decision-model evaluators alike?",
		surface: SurfaceServer,
		events:  []EventName{EventModelConfigured},
		fields: map[EventName][]string{
			EventModelConfigured: {PropModelKind, PropProvider},
		},
	},

	{
		ask:     "Which form fields fail validation most often, and what error categories are shown most?",
		surface: SurfaceUI,
		events:  []EventName{EventFormValidationError, EventErrorDisplayed},
		fields: map[EventName][]string{
			EventFormValidationError: {"field", "errorType"},
			EventErrorDisplayed:      {PropErrorCategory},
		},
	},
	{
		ask:     "What are the most common error categories users experience, at which page locations, and what share of installs hit at least one error in their first session?",
		surface: SurfaceUI,
		events:  []EventName{EventErrorDisplayed},
		fields: map[EventName][]string{
			EventErrorDisplayed: {PropErrorCategory, PropErrorSource, "page", PropTraceID},
		},
	},
	{
		ask:     "LLM call failures by category and provider, which the browser cannot classify on its own.",
		surface: SurfaceServer,
		events:  []EventName{EventAgentRunCompleted},
		fields: map[EventName][]string{
			EventAgentRunCompleted: {PropErrorCategory, PropProvider, PropTraceID},
		},
	},

	{
		ask:     "What share of installs are UI-only, CLI/API-only, or mixed, and how do activation and retention compare across those segments?",
		surface: SurfaceServer,
		events:  []EventName{EventAgentRunCompleted, EventEntrypointUsed},
		fields: map[EventName][]string{
			EventAgentRunCompleted: {PropSubmittedFrom},
			EventEntrypointUsed:    {PropEntrypointType},
		},
	},
	{
		ask:     "The CLI half of the same segmentation: which commands an install actually runs.",
		surface: SurfaceCLI,
		events:  []EventName{EventCLICommandRun},
		fields: map[EventName][]string{
			EventCLICommandRun: {PropCommand},
		},
	},
	{
		ask:     "What are installs actually deploying: how many agents, which entrypoint types, which connector categories, and does composition differ between UI-driven and CLI-driven installs?",
		surface: SurfaceServer,
		events:  []EventName{EventInstallInventory},
		fields: map[EventName][]string{
			EventInstallInventory: {
				PropAgentCount, PropDeployedAgentCount,
				PropEntrypointTypes, PropConnectorCategories,
			},
		},
	},

	{
		ask:     "Which LLM providers and models are installs actually using, hosted versus local, and does model choice correlate with activation and retention?",
		surface: SurfaceServer,
		events:  []EventName{EventModelUsed, EventAgentRunCompleted},
		fields: map[EventName][]string{
			EventModelUsed:         {PropModel, PropProvider, PropModelType},
			EventAgentRunCompleted: {PropModel, PropProvider, PropModelType},
		},
	},

	{
		ask:     "How many users disable telemetry?",
		surface: SurfaceServer,
		events:  []EventName{EventConsentChanged},
		fields: map[EventName][]string{
			EventConsentChanged: {PropConsentPrevious, PropConsentNew},
		},
	},
}

func TestEveryProductQuestionHasItsEvents(t *testing.T) {
	for _, q := range questions {
		events, _ := catalogFor(q.surface)
		if len(events) == 0 {
			t.Errorf("question %q names surface %q, which has no catalog", q.ask, q.surface)
			continue
		}
		for _, name := range q.events {
			if _, ok := events[name]; !ok {
				t.Errorf("question %q needs event %q on the %s surface, which no longer approves it:\n  %s",
					q.ask, name, q.surface, name)
			}
		}
	}
}

func TestEveryProductQuestionHasItsFields(t *testing.T) {
	for _, q := range questions {
		for name, wanted := range q.fields {
			for _, field := range wanted {
				if !carries(q.surface, name, field) {
					t.Errorf("question %q segments %q on %q, which the catalog no longer approves\n  per-event: %v",
						q.ask, name, field, ApprovedFields(q.surface, name))
				}
			}
		}
	}
}

func carries(surface string, name EventName, field string) bool {
	events, universal := catalogFor(surface)
	if _, ok := events[name][field]; ok {
		return true
	}
	_, ok := universal[field]
	return ok
}

func TestEverySurfaceStampsTheRequiredSegmentation(t *testing.T) {
	host := ClientPlatform{OS: "darwin", Arch: "arm64", AppVersion: "1.2.3"}
	for _, surface := range surfaces {
		props := enrich(map[string]any{}, surface, host)
		for _, field := range []string{propOS, propArch, propAppVersion, PropInterface} {
			if _, ok := props[field]; !ok {
				t.Errorf("surface %q does not stamp %q, so no question on it can be segmented by it", surface, field)
			}
		}
		if got := props[PropInterface]; got != surface {
			t.Errorf("surface %q stamped interface = %v, want %q", surface, got, surface)
		}
	}
}

func TestEveryQuestionsFieldsBelongToItsEvents(t *testing.T) {
	for _, q := range questions {
		for name := range q.fields {
			if !slices.Contains(q.events, name) {
				t.Errorf("question %q lists fields for %q but does not name it in events", q.ask, name)
			}
		}
	}
}

func TestTheDeliberatelyUnansweredQuestionsAreStillUnanswered(t *testing.T) {
	unanswered := map[string]string{
		"suggestion:shown":    "getting-started suggestion engagement: no suggestion surface exists to instrument",
		"suggestion:accepted": "getting-started suggestion engagement: no suggestion surface exists to instrument",
		"consent:withdrawn":   "a withdrawal is purged by the flush-time consent re-check, by design",
		"telemetry:opt_out":   "same: a terminal withdrawal cannot be delivered under the consent it revokes",
	}
	for name, why := range unanswered {
		if _, ok := catalog[name]; ok {
			t.Errorf("catalog now approves %q (%s) — add its question to the list in this file", name, why)
		}
	}
	for _, name := range []EventName{EventAgentCreated, EventWorkflowCreated} {
		for _, field := range ApprovedFields(SurfaceServer, name) {
			if strings.EqualFold(field, "templateSource") {
				t.Errorf("%q now approves templateSource — add the template/starter bootstrap question to the list in this file", name)
			}
		}
	}
}
