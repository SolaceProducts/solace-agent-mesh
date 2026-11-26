// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import type { Workflow } from "@/types/workflow";

import { toAgentCardInfo } from "@/features/workflows/toAgentCardInfo";

import { getPaginatedMeta } from "@/testing/helpers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { AgentCardInfo } from "@/types";

import { AgentDisplayCard } from "./AgentDisplayCard";

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] }))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const regularAgent = { name: "hr", displayName: "HR Agent", type: "user", isWorkflow: false } as unknown as AgentCardInfo;

const baseWorkflow: Workflow = {
    id: "wf-1",
    name: "order_flow",
    displayName: "Order Flow",
    description: "",
    appConfig: {},
    deploymentStatus: "not_deployed",
    createdBy: "user",
    updatedBy: "user",
    createdTime: 0,
    updatedTime: 0,
};

function workflowCard() {
    return toAgentCardInfo({ ...baseWorkflow, appConfig: { nodes: [{ id: "n1" }] } });
}

describe("AgentDisplayCard — agent vs workflow branch", () => {
    test("an agent card shows 'Chat With Agent'", () => {
        renderWithProviders(<AgentDisplayCard agent={regularAgent} />);
        expect(screen.getByRole("button", { name: "Chat With Agent" })).toBeInTheDocument();
    });

    test("a workflow card is browsable but offers no chat button", () => {
        renderWithProviders(<AgentDisplayCard agent={workflowCard()} />);
        expect(screen.queryByRole("button", { name: "Chat With Agent" })).not.toBeInTheDocument();
        expect(screen.getByText("Order Flow")).toBeInTheDocument();
    });
});
