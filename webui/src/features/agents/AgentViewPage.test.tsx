// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { createAgent, createDeployedAgent } from "@/testing/data/agents";
import { agentToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta } from "@/testing/helpers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { AgentViewPage } from "./AgentViewPage";

expect.extend(matchers);

const deployedAgent = createDeployedAgent("d1", {
    name: "Deployed Agent",
    description: "A deployed agent",
});

const undeployedAgent = createAgent("u1", {
    name: "Undeployed Agent",
    description: "An undeployed agent",
    deploymentStatus: "not_deployed",
});

const disconnectedAgent = createDeployedAgent("dc1", {
    name: "Disconnected Agent",
    description: "A deployed-but-disconnected agent",
    deploymentStatus: "disconnected",
});

const deploymentV2 = (agentId: string, action: "deploy" | "update") =>
    HttpResponse.json({ data: { id: "dep-1", type: "agentDeployment", agentId, action, status: "success", failure: null, createdBy: "u", createdTime: null, updatedTime: null } }, { status: 201 });

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [deployedAgent, undeployedAgent, disconnectedAgent].map(agentToV2), meta: getPaginatedMeta(3) })),
    http.get("*/api/v2/platform/toolsets", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({}))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderPage(agentId: string, navigate = vi.fn()) {
    return renderWithProviders(<AgentViewPage />, {
        chatContextValues: {
            navigate,
            useLoaderData: (() => ({ agentId })) as unknown as typeof import("react-router-dom").useLoaderData,
        },
    });
}

describe("AgentViewPage — Close button navigation", () => {
    test("deployed agent → /agents?id=<id>&tab=deployed", async () => {
        const navigate = vi.fn();
        renderPage(deployedAgent.id!, navigate);

        const btn = await screen.findByTestId("closeButton");
        await userEvent.click(btn);

        expect(navigate).toHaveBeenCalledWith(`/agents?id=${deployedAgent.id}&tab=deployed`);
    });

    test("undeployed agent → /agents?id=<id>&tab=undeployed", async () => {
        const navigate = vi.fn();
        renderPage(undeployedAgent.id!, navigate);

        const btn = await screen.findByTestId("closeButton");
        await userEvent.click(btn);

        expect(navigate).toHaveBeenCalledWith(`/agents?id=${undeployedAgent.id}&tab=undeployed`);
    });

    test("the Agents breadcrumb leads where Close does", async () => {
        const navigate = vi.fn();
        renderPage(undeployedAgent.id!, navigate);

        await userEvent.click(await screen.findByRole("button", { name: "Agents" }));

        expect(navigate).toHaveBeenCalledWith(`/agents?id=${undeployedAgent.id}&tab=undeployed`);
    });

    test("disconnected agent (deployed row, no heartbeat) → tab=deployed", async () => {
        const navigate = vi.fn();
        renderPage(disconnectedAgent.id!, navigate);

        const btn = await screen.findByTestId("closeButton");
        await userEvent.click(btn);

        expect(navigate).toHaveBeenCalledWith(`/agents?id=${disconnectedAgent.id}&tab=deployed`);
    });

    test("unknown agentId → /agents (no id or tab pinning)", async () => {
        server.use(http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [agentToV2(deployedAgent)], meta: getPaginatedMeta(1) })));
        const navigate = vi.fn();
        renderPage("does-not-exist", navigate);

        const btn = await screen.findByTestId("closeButton");
        await userEvent.click(btn);

        expect(navigate).toHaveBeenCalledWith("/agents");
    });
});

describe("AgentViewPage — Deploy/Undeploy navigation", () => {
    test("confirming Deploy navigates to tab=deployed", async () => {
        const navigate = vi.fn();
        server.use(http.post("*/api/v2/platform/agentDeployments", () => deploymentV2(undeployedAgent.id!, "deploy")));
        renderPage(undeployedAgent.id!, navigate);

        const deployBtn = await screen.findByTestId("deployAgentButton");
        await userEvent.click(deployBtn);

        const confirmBtn = await screen.findByRole("button", { name: /^Deploy$/i });
        await userEvent.click(confirmBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${undeployedAgent.id}&tab=deployed`);
        });
    });

    test("confirming Deploy Update (out-of-sync) navigates to tab=deployed", async () => {
        const outOfSyncAgent = createDeployedAgent("oos1", {
            name: "Out of Sync Agent",
            description: "deployed but out of sync",
            syncStatus: "out_of_sync",
        });
        server.use(
            http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [agentToV2(outOfSyncAgent)], meta: getPaginatedMeta(1) })),
            http.post("*/api/v2/platform/agentDeployments", () => deploymentV2(outOfSyncAgent.id!, "update"))
        );
        const navigate = vi.fn();
        renderPage(outOfSyncAgent.id!, navigate);

        const deployBtn = await screen.findByTestId("deployAgentButton");
        await userEvent.click(deployBtn);

        const confirmBtn = await screen.findByRole("button", { name: /^Deploy$/i });
        await userEvent.click(confirmBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${outOfSyncAgent.id}&tab=deployed`);
        });
    });
});
