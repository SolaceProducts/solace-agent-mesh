// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import type { EvalEvaluator } from "@/types/eval";

import { EvalEvaluatorsPage } from "@/features/evaluations/EvalEvaluatorsPage";

import { evaluatorToV2 } from "@/testing/data/evaluationsV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const evaluators: EvalEvaluator[] = [
    {
        id: "ev-1",
        name: "Answer Relevance",
        description: "Scores how well the response addresses the question.",
        type: "llm_judge",
        isDefault: false,
        promptTemplate: "grade this",
        experimentCount: 2,
        createdBy: "alice@example.com",
        updatedBy: "alice@example.com",
        createdAt: "2026-03-01T10:00:00Z",
        updatedAt: "2026-03-15T12:00:00Z",
    },
];

const evaluatorListHandlers = withDefaults(http.get("*/api/v2/platform/evaluations/evaluators", () => HttpResponse.json(paginatedResponse(evaluators.map(evaluatorToV2)))));

const openRowMenu = async (canvas: ReturnType<typeof within>) => {
    await userEvent.click(await canvas.findByTestId("More"));
    return screen.findByRole("menu");
};

const meta = {
    title: "Evaluations/EvalEvaluatorsPage/Reduced Access",
    component: EvalEvaluatorsPage,
    parameters: {
        docs: {
            description: {
                component: "The evaluators list as seen by an evaluation:_:read holder — the page is reachable, its write affordances are not.",
            },
        },
    },
} satisfies Meta<typeof EvalEvaluatorsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CanRead: Story = {
    parameters: {
        msw: { handlers: evaluatorListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readEvaluations,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Answer Relevance")).toBeInTheDocument();
        expect(canvas.queryByText("+ Create Evaluator")).not.toBeInTheDocument();

        const menu = await openRowMenu(canvas);
        expect(within(menu).getByText("Open Evaluator")).toBeInTheDocument();
        expect(within(menu).queryByText("Create Experiment")).not.toBeInTheDocument();
        expect(within(menu).queryByText("Edit")).not.toBeInTheDocument();
        expect(within(menu).queryByText("Delete")).not.toBeInTheDocument();
    },
};

export const CanCreate: Story = {
    parameters: {
        msw: { handlers: evaluatorListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readEvaluations || capability === CAPABILITIES.createEvaluations,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("+ Create Evaluator")).toBeInTheDocument();

        const menu = await openRowMenu(canvas);
        expect(within(menu).getByText("Create Experiment")).toBeInTheDocument();
        expect(within(menu).queryByText("Edit")).not.toBeInTheDocument();
        expect(within(menu).queryByText("Delete")).not.toBeInTheDocument();
    },
};

export const CanUpdateAndDelete: Story = {
    parameters: {
        msw: { handlers: evaluatorListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: () => true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openRowMenu(canvas);
        expect(within(menu).getByText("Open Evaluator")).toBeInTheDocument();
        expect(within(menu).getByText("Edit")).toBeInTheDocument();
        expect(within(menu).getByText("Delete")).toBeInTheDocument();
    },
};

const builtins: EvalEvaluator[] = [
    { ...evaluators[0], id: "ev-judge", name: "Factuality", isDefault: true, createdBy: "system", updatedBy: "system" },
    { ...evaluators[0], id: "ev-heuristic", name: "Valid JSON", type: "heuristic", heuristicType: "valid_json", promptTemplate: undefined, isDefault: true, createdBy: "system", updatedBy: "system" },
];

const builtinListHandlers = withDefaults(http.get("*/api/v2/platform/evaluations/evaluators", () => HttpResponse.json(paginatedResponse(builtins.map(evaluatorToV2)))));

export const BuiltInRowMenus: Story = {
    parameters: {
        msw: { handlers: builtinListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: () => true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Valid JSON")).toBeInTheDocument();
        expect(canvas.getAllByTestId("More")).toHaveLength(1);

        const menu = await openRowMenu(canvas);
        expect(within(menu).getByText("Open Evaluator")).toBeInTheDocument();
        expect(within(menu).getByText("Create Experiment")).toBeInTheDocument();
        expect(within(menu).getByText("Edit Model")).toBeInTheDocument();
        expect(within(menu).queryByText("Edit")).not.toBeInTheDocument();
        expect(within(menu).queryByText("Delete")).not.toBeInTheDocument();
    },
};

export const BuiltInJudgeCanRead: Story = {
    parameters: {
        msw: { handlers: builtinListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readEvaluations,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openRowMenu(canvas);
        expect(within(menu).getByText("Open Evaluator")).toBeInTheDocument();
        expect(within(menu).queryByText("Create Experiment")).not.toBeInTheDocument();
        expect(within(menu).queryByText("Edit Model")).not.toBeInTheDocument();
    },
};
