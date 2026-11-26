// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import type { EvalEvaluator } from "@/types/eval";

import { evaluatorToV2 } from "@/testing/data/evaluationsV2";
import { anthropicModelConfig, typesafeDecisionModelConfig } from "@/testing/data/models";
import { toModelV2 } from "@/testing/data/modelsV2";
import { paginatedResponse } from "@/testing/helpers/handlers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { CreateEvaluatorDialog } from "./CreateEvaluatorDialog";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const FLAG = "eval_decision_evaluator";

const judge: EvalEvaluator = {
    id: "judge-1",
    name: "Strict Judge",
    type: "llm_judge",
    isDefault: false,
    experimentCount: 0,
    createdBy: "u",
    updatedBy: "u",
    createdAt: "",
    updatedAt: "",
};

function setup(captured: { body?: Record<string, unknown> }, createResponse?: () => Response) {
    server.use(
        http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([anthropicModelConfig, typesafeDecisionModelConfig].map(toModelV2)))),
        http.get("*/api/v2/platform/evaluations/evaluators", () => HttpResponse.json(paginatedResponse([judge, { ...judge, id: "heur", name: "Exact Match", type: "heuristic" as const }].map(evaluatorToV2)))),
        http.post("*/api/v2/platform/evaluations/evaluators", async ({ request }) => {
            captured.body = (await request.json()) as Record<string, unknown>;
            return createResponse?.() ?? HttpResponse.json({ data: evaluatorToV2({ ...judge, id: "new-ev", type: "decision" }) }, { status: 201 });
        })
    );
}

function renderDialog(flagOn: boolean, onCreated = vi.fn()) {
    renderWithProviders(<CreateEvaluatorDialog open onOpenChange={vi.fn()} onCreated={onCreated} />, { featureFlags: { [FLAG]: flagOn } });
    return onCreated;
}

async function pickOption(user: ReturnType<typeof userEvent.setup>, trigger: HTMLElement, name: string | RegExp) {
    await user.click(trigger);
    await user.click(await screen.findByRole("option", { name }));
}

async function fillDecisionBasics(user: ReturnType<typeof userEvent.setup>) {
    await user.type(screen.getByPlaceholderText("e.g. Quality Judge"), "Grounded");
    await user.type(screen.getByPlaceholderText("Describe what this evaluator assesses"), "Checks grounding");
    await user.click(screen.getByRole("radio", { name: /Decision Model as a Judge/ }));
    await pickOption(user, await screen.findByTestId("decisionModelSelect"), /jev/);
    await user.type(screen.getByRole("textbox", { name: "Question" }), "Is the response grounded?");
}

describe("CreateEvaluatorDialog — decision evaluators", () => {
    test("offers no type choice while decision evaluators are off", async () => {
        setup({});
        renderDialog(false);

        expect(await screen.findByTestId("evaluatorTypeReadOnly")).toHaveTextContent("LLM as a Judge");
        expect(screen.queryByRole("radiogroup", { name: "Evaluator Type" })).toBeNull();
        expect(screen.queryByTestId("decisionEvaluatorFields")).toBeNull();
        expect(screen.getByPlaceholderText(/Evaluate whether the response/)).toBeInTheDocument();
    });

    test("switching to a decision model swaps the criteria for the question builder, offering only decision models", async () => {
        setup({});
        renderDialog(true);
        const user = userEvent.setup();

        expect(screen.getByRole("radio", { name: /LLM as a Judge/ })).toBeChecked();
        await user.click(screen.getByRole("radio", { name: /Decision Model as a Judge/ }));

        expect(await screen.findByTestId("decisionEvaluatorFields")).toBeInTheDocument();
        expect(screen.queryByPlaceholderText(/Evaluate whether the response/)).toBeNull();

        await user.click(screen.getByTestId("decisionModelSelect"));
        expect(await screen.findByRole("option", { name: /jev/ })).toBeInTheDocument();
        expect(screen.queryByRole("option", { name: /anthropic-model/ })).toBeNull();
    });

    test("the question type is a dropdown of three types, each with its description, starting on Choice", async () => {
        setup({});
        renderDialog(true);
        const user = userEvent.setup();
        await user.click(screen.getByRole("radio", { name: /Decision Model as a Judge/ }));

        const questionType = await screen.findByRole("combobox", { name: "Question Type" });
        expect(questionType).toHaveTextContent(/^Choice$/);
        expect(screen.getByRole("textbox", { name: "Option 1" })).toBeInTheDocument();

        await user.click(questionType);
        const options = await screen.findAllByRole("option");
        expect(options.map(o => o.textContent)).toEqual([
            "ChoicePicks one option; each option has its own score.",
            "ScoreRates on ordered levels; the lowest scores 0% and the highest 100%.",
            "BooleanAnswers yes or no; the score is the probability that the answer is true.",
        ]);
        expect(screen.getByRole("option", { name: /^Choice/ })).toHaveAttribute("aria-selected", "true");

        await user.click(screen.getByRole("option", { name: /^Score/ }));
        expect(questionType).toHaveTextContent(/^Score$/);
        expect(screen.getByRole("textbox", { name: "Level 1" })).toHaveValue("poor");
        expect(screen.queryByRole("textbox", { name: "Option 1" })).toBeNull();
        expect(screen.getByRole("radiogroup", { name: "Scoring" })).toBeInTheDocument();

        await pickOption(user, questionType, /^Boolean/);
        expect(screen.getByRole("textbox", { name: "True description" })).toBeInTheDocument();
        expect(screen.queryByRole("radiogroup", { name: "Scoring" })).toBeNull();
    });

    test("creates a choice evaluator with options, scores, reads and escalation", async () => {
        const captured: { body?: Record<string, unknown> } = {};
        setup(captured);
        const onCreated = renderDialog(true);
        const user = userEvent.setup();

        await fillDecisionBasics(user);

        const option1 = screen.getByRole("textbox", { name: "Option 1" });
        await user.clear(option1);
        await user.type(option1, "made_up");
        await user.type(screen.getByRole("textbox", { name: "Option 1 description" }), "Not in the sources");
        await user.click(screen.getByRole("button", { name: "Add Option" }));
        await user.type(screen.getByRole("textbox", { name: "Option 3" }), "partly");
        const score3 = screen.getByRole("spinbutton", { name: "Option 3 score" });
        await user.clear(score3);
        await user.type(score3, "50");
        await user.click(screen.getByRole("button", { name: "Remove partly" }));
        expect(screen.queryByRole("textbox", { name: "Option 3" })).toBeNull();

        await user.click(screen.getByRole("radio", { name: /Weighted by probability/ }));
        await user.click(screen.getByRole("checkbox", { name: /Transcript/ }));
        await user.click(screen.getByRole("checkbox", { name: "Expected response" }));
        expect(screen.getByRole("checkbox", { name: "Response" })).toBeDisabled();

        await pickOption(user, screen.getByTestId("escalateToSelect"), "Strict Judge");
        expect(screen.queryByRole("option", { name: "Exact Match" })).toBeNull();
        await user.type(screen.getByRole("spinbutton", { name: "Minimum confidence" }), "80");

        await user.click(screen.getByRole("button", { name: "Create" }));

        await waitFor(() => expect(onCreated).toHaveBeenCalledWith("new-ev"));
        expect(captured.body).toEqual({
            name: "Grounded",
            description: "Checks grounding",
            evaluatorType: "decision",
            model: typesafeDecisionModelConfig.id,
            question: {
                type: "choice",
                instructions: "Is the response grounded?",
                criteria: [{ key: "made_up", description: "Not in the sources" }, { key: "pass" }],
            },
            scoring: "expected",
            reads: ["prompt", "response", "transcript"],
            choiceScores: { made_up: 0, pass: 1 },
            escalateToEvaluatorId: "judge-1",
            minConfidence: 0.8,
        });
    });

    test("creates a boolean evaluator without choiceScores or scoring choice", async () => {
        const captured: { body?: Record<string, unknown> } = {};
        setup(captured);
        const onCreated = renderDialog(true);
        const user = userEvent.setup();

        await fillDecisionBasics(user);
        await pickOption(user, screen.getByRole("combobox", { name: "Question Type" }), /^Boolean/);
        expect(screen.queryByRole("radiogroup", { name: "Scoring" })).toBeNull();
        await user.type(screen.getByRole("textbox", { name: "True description" }), "grounded");

        await user.click(screen.getByRole("button", { name: "Create" }));
        expect(await screen.findByText("Describe both true and false, or neither.")).toBeInTheDocument();
        expect(captured.body).toBeUndefined();

        await user.type(screen.getByRole("textbox", { name: "False description" }), "made up");
        await user.click(screen.getByRole("button", { name: "Create" }));
        await waitFor(() => expect(onCreated).toHaveBeenCalled());
        expect(captured.body).toMatchObject({
            evaluatorType: "decision",
            question: {
                type: "boolean",
                criteria: [
                    { key: "true", description: "grounded" },
                    { key: "false", description: "made up" },
                ],
            },
            reads: ["prompt", "response", "expected_response"],
        });
        expect(captured.body).not.toHaveProperty("choiceScores");
    });

    test("creates a score evaluator from its levels, lowest first", async () => {
        const captured: { body?: Record<string, unknown> } = {};
        setup(captured);
        const onCreated = renderDialog(true);
        const user = userEvent.setup();

        await fillDecisionBasics(user);
        await pickOption(user, screen.getByRole("combobox", { name: "Question Type" }), /^Score/);
        await user.click(screen.getByRole("button", { name: "Add Level" }));
        await user.type(screen.getByRole("textbox", { name: "Level 4" }), "excellent");
        await user.type(screen.getByRole("textbox", { name: "Level 4 description" }), "Flawless");
        await user.click(screen.getByRole("button", { name: "Remove poor" }));

        await user.click(screen.getByRole("button", { name: "Create" }));
        await waitFor(() => expect(onCreated).toHaveBeenCalled());
        expect(captured.body?.question).toEqual({ type: "score", instructions: "Is the response grounded?", criteria: [{ key: "fair" }, { key: "good" }, { key: "excellent", description: "Flawless" }] });
        expect(captured.body).not.toHaveProperty("choiceScores");
    });

    test("shows the platform's field errors beside their fields", async () => {
        const captured: { body?: Record<string, unknown> } = {};
        setup(captured, () => HttpResponse.json({ message: "Validation failed", validationDetails: { model: ['model "jev" is a chat model'], escalateToEvaluatorId: ["not a judge"] } }, { status: 422 }));
        renderDialog(true);
        const user = userEvent.setup();

        await fillDecisionBasics(user);
        await user.click(screen.getByRole("button", { name: "Create" }));

        const modelError = await screen.findByText('model "jev" is a chat model');
        expect(modelError).toHaveAttribute("role", "alert");
        expect(screen.getByTestId("decisionModelSelect")).toHaveAttribute("aria-describedby", modelError.id);
        const escalateError = screen.getByText("not a judge");
        expect(screen.getByTestId("escalateToSelect")).toHaveAttribute("aria-describedby", escalateError.id);
        expect(screen.getByText(/Some settings need attention/)).toBeInTheDocument();
    });

    test("points to the Models page when no decision model is registered", async () => {
        setup({});
        server.use(http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([toModelV2(anthropicModelConfig)]))));
        renderDialog(true);
        const user = userEvent.setup();

        await user.click(screen.getByRole("radio", { name: /Decision Model as a Judge/ }));
        const empty = await screen.findByTestId("noDecisionModels");
        expect(within(empty).getByRole("link", { name: /Models page/ })).toHaveAttribute("href", "/models");
    });
});
