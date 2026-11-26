// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import type { WatchlistAgentChart as WatchlistAgentChartType } from "@/types/eval";

import { agentToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { WatchlistAgentChart } from "./WatchlistAgentChart";

const AGENT_ID = "019ddee5-159d-7f10-9d1c-c8ac439386ed";
const AGENTS_HANDLER = http.get("*/api/v2/platform/agents", () =>
    HttpResponse.json({
        data: [agentToV2({ id: AGENT_ID, name: "MermaidAgent", instanceName: "MermaidAgent", description: "" })],
        meta: getPaginatedMeta(1, 20),
    })
);

function makePoint(runGroupId: string, hoursAgo: number, score: number | null) {
    return {
        runGroupId,
        startedAt: new Date(Date.now() - hoursAgo * 3_600_000).toISOString(),
        score,
    };
}

const RICH_AGENT: WatchlistAgentChartType = {
    agentName: "MermaidAgent",
    resolvedModelProvider: "general",
    isBuiltIn: false,
    position: 0,
    experiments: [
        {
            experimentId: "exp-quality",
            experimentName: "Quality",
            primaryEvaluatorId: "ev-1",
            primaryEvaluatorName: "LLM Judge",
            points: [makePoint("g1", 96, 0.55), makePoint("g2", 72, 0.62), makePoint("g3", 48, 0.71), makePoint("g4", 24, 0.78)],
        },
        {
            experimentId: "exp-factuality",
            experimentName: "Factuality",
            primaryEvaluatorId: "ev-2",
            primaryEvaluatorName: "Factuality",
            points: [makePoint("g1", 96, 0.5), makePoint("g2", 72, null), makePoint("g3", 48, 0.66), makePoint("g4", 24, 0.72)],
        },
    ],
};

const BUILT_IN_AGENT: WatchlistAgentChartType = {
    ...RICH_AGENT,
    agentName: "OrchestratorAgent",
    isBuiltIn: true,
};

const EMPTY_AGENT: WatchlistAgentChartType = {
    agentName: "MarkitdownAgent",
    resolvedModelProvider: "general",
    isBuiltIn: false,
    position: 0,
    experiments: [],
};

const meta = {
    title: "Evaluations/WatchlistAgentChart",
    component: WatchlistAgentChart,
    parameters: {
        layout: "padded",
        msw: { handlers: withDefaults(AGENTS_HANDLER) },
        docs: {
            description: {
                component:
                    "Per-agent line chart for the evaluations watchlist. One line per experiment, scored on the experiment's primary evaluator. Bridges null points via connectNulls and falls back to an empty-state when the agent has no qualifying experiments.",
            },
        },
    },
    args: { agent: RICH_AGENT },
    decorators: [
        (Story: React.ComponentType) => (
            <div className="w-180">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof WatchlistAgentChart>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const link = await canvas.findByRole("link", { name: "MermaidAgent" });
        await expect(link).toHaveAttribute("href", `/agent-mesh/${AGENT_ID}`);

        await expect(canvas.getAllByText(/Quality/).length).toBeGreaterThan(0);
        await expect(canvas.getByText(/\(LLM Judge\)/)).toBeInTheDocument();
        await expect(canvas.getByText(/\(Factuality\)/)).toBeInTheDocument();
    },
};

export const FullWidthVariant: Story = {
    args: { fullWidth: true },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByRole("link", { name: "MermaidAgent" });
    },
};

export const BuiltInAgentNotClickable: Story = {
    args: { agent: BUILT_IN_AGENT },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("OrchestratorAgent");
        await expect(canvas.queryByRole("link", { name: "OrchestratorAgent" })).not.toBeInTheDocument();
    },
};

export const EmptyExperimentsShowsPlaceholder: Story = {
    args: { agent: EMPTY_AGENT },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText("No experiments yet for this model")).toBeInTheDocument();
        await expect(canvas.queryByText(/Quality/)).not.toBeInTheDocument();
    },
};
