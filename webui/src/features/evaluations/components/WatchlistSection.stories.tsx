// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { agentCardKeys } from "@/api/agent-cards";
import { agentKeys } from "@/api/agents";
import { remoteAgentKeys } from "@/api/remote-agents";

import { queryClient } from "@/providers/query/QueryClient";

import type { WatchlistAgentChart } from "@/types/eval";

import { watchlistChartToV2 } from "@/testing/data/evaluationsV2";
import { agentToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta, paginatedResponse, withDefaults } from "@/testing/helpers";

import { WatchlistSection } from "./WatchlistSection";

const CHART_URL = "*/api/v2/platform/evaluations/watchlist/chartData";
const WATCHLIST_URL = "*/api/v2/platform/evaluations/watchlist";

function makePoint(runGroupId: string, hoursAgo: number, score: number | null) {
    return {
        runGroupId,
        startedAt: new Date(Date.now() - hoursAgo * 3_600_000).toISOString(),
        score,
    };
}

function makeAgentChart(name: string, position: number): WatchlistAgentChart {
    return {
        agentName: name,
        resolvedModelProvider: "general",
        isBuiltIn: false,
        position,
        experiments: [
            {
                experimentId: `${name}-quality`,
                experimentName: "Quality",
                primaryEvaluatorId: "ev-1",
                primaryEvaluatorName: "LLM Judge",
                points: [makePoint("g1", 72, 0.6), makePoint("g2", 48, 0.7), makePoint("g3", 24, 0.8)],
            },
        ],
    };
}

const handlersEmpty = withDefaults(
    http.get(CHART_URL, () => HttpResponse.json(paginatedResponse([]))),
    http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse([])))
);

const handlersOneAgent = withDefaults(
    http.get(CHART_URL, () => HttpResponse.json(paginatedResponse([makeAgentChart("MermaidAgent", 0)].map(watchlistChartToV2)))),
    http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse([]))),
    http.get("*/api/v2/platform/agents", () =>
        HttpResponse.json({
            data: [agentToV2({ id: "agent-1", name: "MermaidAgent", instanceName: "MermaidAgent", description: "" })],
            meta: getPaginatedMeta(1, 20),
        })
    )
);

const handlersMultipleAgents = withDefaults(
    http.get(CHART_URL, () => HttpResponse.json(paginatedResponse([makeAgentChart("MermaidAgent", 0), makeAgentChart("WebAgent", 1), makeAgentChart("MarkitdownAgent", 2)].map(watchlistChartToV2)))),
    http.get(WATCHLIST_URL, () => HttpResponse.json(paginatedResponse([]))),
    http.get("*/api/v2/platform/agents", () =>
        HttpResponse.json({
            data: [
                agentToV2({ id: "agent-1", name: "MermaidAgent", instanceName: "MermaidAgent", description: "" }),
                agentToV2({ id: "agent-2", name: "WebAgent", instanceName: "WebAgent", description: "" }),
                agentToV2({ id: "agent-3", name: "MarkitdownAgent", instanceName: "MarkitdownAgent", description: "" }),
            ],
            meta: getPaginatedMeta(3, 20),
        })
    )
);

const meta = {
    title: "Evaluations/WatchlistSection",
    component: WatchlistSection,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Watchlist section on the Evaluations overview page. First watched agent is full-width; the rest fall into a 2-col grid. Empty state nudges users to run an experiment.",
            },
        },
    },
    decorators: [
        (Story: React.ComponentType) => (
            <div className="w-250">
                <Story />
            </div>
        ),
    ],
    beforeEach: () => {
        queryClient.removeQueries({ queryKey: agentKeys.lists() });
        queryClient.removeQueries({ queryKey: remoteAgentKeys.lists() });
        queryClient.removeQueries({ queryKey: agentCardKeys.list() });
    },
} satisfies Meta<typeof WatchlistSection>;

export default meta;
type Story = StoryObj<typeof meta>;

export const EmptyState: Story = {
    parameters: { msw: { handlers: handlersEmpty } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await waitFor(async () => {
            await expect(canvas.getByText(/Add agents to the Watchlist/i)).toBeInTheDocument();
        });
        await expect(canvas.getByRole("button", { name: /Edit Watchlist/i })).toBeInTheDocument();
    },
};

export const OneAgent: Story = {
    parameters: { msw: { handlers: handlersOneAgent } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await waitFor(async () => {
            await expect(canvas.getByRole("link", { name: "MermaidAgent" })).toBeInTheDocument();
        });
        await expect(canvas.queryByText(/Add agents to the Watchlist/i)).not.toBeInTheDocument();
    },
};

export const MultipleAgents: Story = {
    parameters: { msw: { handlers: handlersMultipleAgents } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await waitFor(async () => {
            await expect(canvas.getByRole("link", { name: "MermaidAgent" })).toBeInTheDocument();
            await expect(canvas.getByRole("link", { name: "WebAgent" })).toBeInTheDocument();
            await expect(canvas.getByRole("link", { name: "MarkitdownAgent" })).toBeInTheDocument();
        });
    },
};

export const EditButtonOpensDialog: Story = {
    parameters: { msw: { handlers: handlersEmpty } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByRole("button", { name: /Edit Watchlist/i });
        await userEvent.click(canvas.getByRole("button", { name: /Edit Watchlist/i }));

        await waitFor(async () => {
            await expect(screen.getByRole("dialog")).toBeInTheDocument();
        });
        await expect(screen.getByText(/Select up to 5 agents to display on your Watchlist/i)).toBeInTheDocument();
    },
};
