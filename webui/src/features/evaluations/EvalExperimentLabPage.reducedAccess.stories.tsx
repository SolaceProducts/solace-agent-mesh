// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import type { EvalDataset, EvalEvaluator, EvalExperiment } from "@/types/eval";

import { EvalExperimentLabPage } from "@/features/evaluations/EvalExperimentLabPage";

import { datasetToV2, evaluatorToV2, experimentToV2 } from "@/testing/data/evaluationsV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const datasets: EvalDataset[] = [
    {
        id: "ds-1",
        name: "Support Questions",
        description: "Common customer support prompts.",
        exampleCount: 12,
        experimentCount: 1,
        createdBy: "alice@example.com",
        updatedBy: "alice@example.com",
        createdAt: "2026-03-01T10:00:00Z",
        updatedAt: "2026-03-15T12:00:00Z",
    },
];

const evaluators: EvalEvaluator[] = [
    {
        id: "ev-1",
        name: "Answer Relevance",
        description: "Scores how well the response addresses the question.",
        type: "llm_judge",
        isDefault: false,
        promptTemplate: "grade this",
        experimentCount: 1,
        createdBy: "alice@example.com",
        updatedBy: "alice@example.com",
        createdAt: "2026-03-01T10:00:00Z",
        updatedAt: "2026-03-15T12:00:00Z",
    },
];

const experiments: EvalExperiment[] = [];

const labHandlers = withDefaults(
    http.get("*/api/v2/platform/evaluations/experiments", () => HttpResponse.json(paginatedResponse(experiments.map(experimentToV2)))),
    http.get("*/api/v2/platform/evaluations/datasets", () => HttpResponse.json(paginatedResponse(datasets.map(datasetToV2)))),
    http.get("*/api/v2/platform/evaluations/evaluators", () => HttpResponse.json(paginatedResponse(evaluators.map(evaluatorToV2))))
);

const dismissQuickStart = () => globalThis.localStorage.setItem("eval.experimentsQuickStartDismissed", "true");

const readOnly = (capability: Capability) => capability === CAPABILITIES.readEvaluations;

const openTab = (canvas: ReturnType<typeof within>, name: string) => userEvent.click(canvas.getByRole("tab", { name }));

const meta = {
    title: "Evaluations/EvalExperimentLabPage/Reduced Access",
    component: EvalExperimentLabPage,
    beforeEach: dismissQuickStart,
    parameters: {
        msw: { handlers: labHandlers },
        routerValues: { initialPath: "/evaluations/lab" },
        docs: {
            description: {
                component:
                    "The Experiment Lab as seen by an evaluation:_:read holder — every tab is reachable, none of the create affordances are. Reaching the route needs only evaluation:_:read, so the header must gate on evaluation:_:create itself.",
            },
        },
    },
} satisfies Meta<typeof EvalExperimentLabPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CanRead: Story = {
    parameters: {
        userContext: { user: { capabilities: [] }, userHasCapability: readOnly },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByRole("tab", { name: "Experiments" })).toBeInTheDocument();
        expect(canvas.queryByRole("button", { name: "Create Experiment" })).not.toBeInTheDocument();

        await openTab(canvas, "Datasets");
        expect(await canvas.findByText("Support Questions")).toBeInTheDocument();
        expect(canvas.queryByRole("button", { name: /Create New Dataset/ })).not.toBeInTheDocument();
        expect(canvas.queryByTestId("More")).not.toBeInTheDocument();

        await openTab(canvas, "Evaluators");
        expect(await canvas.findByText("Answer Relevance")).toBeInTheDocument();
        expect(canvas.queryByRole("button", { name: /Create Evaluator/ })).not.toBeInTheDocument();
    },
};

export const CanReadWithNoDatasets: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/evaluations/datasets", () => HttpResponse.json(paginatedResponse([]))),
                http.get("*/api/v2/platform/evaluations/experiments", () => HttpResponse.json(paginatedResponse(experiments.map(experimentToV2)))),
                http.get("*/api/v2/platform/evaluations/evaluators", () => HttpResponse.json(paginatedResponse(evaluators.map(evaluatorToV2))))
            ),
        },
        userContext: { user: { capabilities: [] }, userHasCapability: readOnly },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await openTab(canvas, "Datasets");
        expect(await canvas.findByText("Create Your First Dataset")).toBeInTheDocument();
        expect(canvas.queryByRole("button", { name: /Create New Dataset/ })).not.toBeInTheDocument();
    },
};

export const CanCreate: Story = {
    parameters: {
        userContext: {
            user: { capabilities: [] },
            userHasCapability: (capability: Capability) => readOnly(capability) || capability === CAPABILITIES.createEvaluations,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByRole("button", { name: "Create Experiment" })).toBeInTheDocument();

        await openTab(canvas, "Datasets");
        expect(await canvas.findByRole("button", { name: /Create New Dataset/ })).toBeInTheDocument();

        await openTab(canvas, "Evaluators");
        expect(await canvas.findByRole("button", { name: /Create Evaluator/ })).toBeInTheDocument();

        await openTab(canvas, "Datasets");
        expect(canvas.queryByTestId("More")).not.toBeInTheDocument();
    },
};

export const CanDelete: Story = {
    parameters: {
        userContext: { user: { capabilities: [] }, userHasCapability: () => true },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await openTab(canvas, "Datasets");
        await userEvent.click(await canvas.findByTestId("More"));
        expect(within(await screen.findByRole("menu")).getByText("Delete")).toBeInTheDocument();
    },
};
