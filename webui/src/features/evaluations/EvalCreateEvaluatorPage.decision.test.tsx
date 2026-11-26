// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import type { EvalEvaluator } from "@/types/eval";

import { evaluatorToV2 } from "@/testing/data/evaluationsV2";
import { anthropicModelConfig, typesafeDecisionModelConfig } from "@/testing/data/models";
import { toModelV2 } from "@/testing/data/modelsV2";
import { paginatedResponse } from "@/testing/helpers/handlers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

let loaderData: { evaluatorId?: string } = {};

vi.mock("react-router-dom", async () => {
    const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
    return { ...actual, useLoaderData: () => loaderData };
});

import { EvalCreateEvaluatorPage } from "./EvalCreateEvaluatorPage";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => {
    server.resetHandlers();
    loaderData = {};
});
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

const decisionEvaluator: EvalEvaluator = {
    ...judge,
    id: "dec-1",
    name: "Grounded",
    description: "Checks grounding",
    type: "decision",
    model: "jev",
    question: { type: "choice", instructions: "Is the response grounded?", criteria: [{ key: "grounded", description: "Cites sources" }, { key: "made_up" }] },
    choiceScores: { grounded: 1, made_up: 0 },
    scoring: "chosen",
    reads: ["prompt", "response", "transcript"],
    minConfidence: 0.8,
    escalateToEvaluatorId: "judge-1",
};

function setup(captured: { saved?: Record<string, unknown> }, evaluator: EvalEvaluator = decisionEvaluator) {
    server.use(
        http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([anthropicModelConfig, typesafeDecisionModelConfig].map(toModelV2)))),
        http.get("*/api/v2/platform/evaluations/evaluators", () => HttpResponse.json(paginatedResponse([judge, evaluator].map(evaluatorToV2)))),
        http.get("*/api/v2/platform/evaluations/evaluators/:id", () => HttpResponse.json({ data: evaluatorToV2(evaluator) })),
        http.get("*/api/v2/platform/evaluations/experiments", () => HttpResponse.json(paginatedResponse([]))),
        http.patch("*/api/v2/platform/evaluations/evaluators/:id", async ({ request }) => {
            captured.saved = (await request.json()) as Record<string, unknown>;
            return HttpResponse.json({ data: { ...evaluatorToV2(evaluator), ...captured.saved } });
        })
    );
}

function renderPage(flagOn: boolean, editing = false) {
    loaderData = { evaluatorId: "dec-1" };
    const entry = editing ? { pathname: "/evaluations/evaluators/dec-1", state: { editing: true } } : "/evaluations/evaluators/dec-1";
    return render(
        <StoryProvider flagValues={{ [FLAG]: flagOn }}>
            <MemoryRouter initialEntries={[entry]}>
                <Routes>
                    <Route path="/evaluations/evaluators/:evaluatorId" element={<EvalCreateEvaluatorPage />} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("EvalCreateEvaluatorPage — decision evaluators", () => {
    test("the read-only view shows the decision settings", async () => {
        setup({});
        renderPage(true);

        const summary = await screen.findByTestId("decisionEvaluatorSummary");
        expect(screen.getByText("Decision Model as a Judge")).toBeInTheDocument();
        expect(within(summary).getByText("Is the response grounded?")).toBeInTheDocument();
        expect(within(summary).getByText("Cites sources")).toBeInTheDocument();
        expect(within(summary).getByText("100%")).toBeInTheDocument();
        expect(within(summary).getByText("Chosen answer")).toBeInTheDocument();
        expect(within(summary).getByText(/Transcript/)).toBeInTheDocument();
        expect(await within(summary).findByText("Strict Judge, below 80% confidence or on provider errors other than a rejected or denied API key or a configuration error")).toBeInTheDocument();
        expect(await within(summary).findByText("jev-1.13.0")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Edit/ })).toBeInTheDocument();
        expect(screen.queryByText("Criteria")).toBeNull();
    });

    test("offers no edit while decision evaluators are off, even when asked to edit, but still offers delete", async () => {
        setup({});
        renderPage(false, true);
        const user = userEvent.setup();

        expect(await screen.findByTestId("decisionEvaluatorSummary")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Edit/ })).toBeNull();
        expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
        await user.click(screen.getByRole("button", { name: "More" }));
        expect(await screen.findByRole("menuitem", { name: "Delete" })).toBeInTheDocument();
    });

    test("a rename saves an exact fractional threshold back unchanged", async () => {
        const captured: { saved?: Record<string, unknown> } = {};
        setup(captured, { ...decisionEvaluator, minConfidence: 0.875 });
        renderPage(true, true);
        const user = userEvent.setup();

        expect(await screen.findByRole("spinbutton", { name: "Minimum confidence" })).toHaveValue(87.5);
        const name = screen.getByDisplayValue("Grounded");
        await user.clear(name);
        await user.type(name, "Grounded v2");
        await user.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(captured.saved).toBeDefined());
        expect(captured.saved).toMatchObject({ name: "Grounded v2", minConfidence: 0.875, escalateToEvaluatorId: "judge-1", choiceScores: { grounded: 1, made_up: 0 } });
    });

    test("editing keeps the type read-only and saves every setting, clearing what the form leaves unset", async () => {
        const captured: { saved?: Record<string, unknown> } = {};
        setup(captured);
        renderPage(true, true);
        const user = userEvent.setup();

        expect(await screen.findByDisplayValue("Is the response grounded?")).toBeInTheDocument();
        expect(screen.getByTestId("evaluatorTypeReadOnly")).toHaveTextContent("Decision Model as a Judge");
        expect(screen.queryByRole("radiogroup", { name: "Evaluator Type" })).toBeNull();

        await user.click(screen.getByRole("combobox", { name: "Question Type" }));
        await user.click(await screen.findByRole("option", { name: /^Boolean/ }));
        await user.click(screen.getByTestId("escalateToSelect"));
        await user.click(await screen.findByRole("option", { name: "No escalation" }));
        expect(screen.queryByRole("spinbutton", { name: "Minimum confidence" })).toBeNull();

        await user.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(captured.saved).toBeDefined());
        expect(captured.saved).toEqual({
            name: "Grounded",
            description: "Checks grounding",
            model: "jev",
            question: { type: "boolean", instructions: "Is the response grounded?" },
            scoring: "chosen",
            reads: ["prompt", "response", "transcript"],
            choiceScores: null,
            escalateToEvaluatorId: null,
            minConfidence: null,
        });
    });

    test("discarding restores the stored settings", async () => {
        setup({});
        renderPage(true, true);
        const user = userEvent.setup();

        const question = await screen.findByDisplayValue("Is the response grounded?");
        await user.clear(question);
        await user.type(question, "Something else");
        await user.click(screen.getByRole("button", { name: "Discard Changes" }));

        expect(await screen.findByTestId("decisionEvaluatorSummary")).toBeInTheDocument();
        expect(screen.getByText("Is the response grounded?")).toBeInTheDocument();
    });
});
