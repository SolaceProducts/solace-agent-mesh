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

import { agentToV2, remoteAgentToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta } from "@/testing/helpers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Agent, AgentCardInfo, RemoteAgent } from "@/types";

import { AgentCardComparisonDialog } from "./AgentCardComparisonDialog";

expect.extend(matchers);

const AGENT_ID = "cmp-agent-1";
const REMOTE_AGENT_ID = "cmp-remote-1";

const userAgent: Agent = {
    id: AGENT_ID,
    name: "My Agent",
    description: "Test agent",
    deploymentStatus: "deployed",
    systemPrompt: "You are helpful.",
    modelProvider: ["general"],
};

const remoteUserAgent: RemoteAgent = {
    id: REMOTE_AGENT_ID,
    name: "My Remote Agent",
    description: "Test remote agent",
    agentUrl: "https://example.com/agent",
    agentAuthType: "none",
    agentCardLocation: "well_known",
    agentCardUrl: "https://example.com/agent/.well-known/agent-card.json",
    deploymentStatus: "deployed",
    runtimeStatus: { status: "running" },
    syncStatus: "out_of_sync",
    requestTimeoutSeconds: 60,
    createdBy: "user@example.com",
    updatedBy: "user@example.com",
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-02T00:00:00.000Z",
};

const makeCard = (overrides: Partial<AgentCardInfo> = {}): AgentCardInfo =>
    ({
        name: `agent_${AGENT_ID}`,
        displayName: "My Agent",
        description: "Test agent",
        type: "user",
        status: "deployed",
        capabilities: { skills: [], extensions: [] },
        defaultInputModes: ["text"],
        defaultOutputModes: ["text"],
        protocolVersion: "1.0",
        url: "http://localhost",
        ...overrides,
    }) as AgentCardInfo;

const deployedCard = makeCard({ status: "deployed" });
const draftCard = makeCard({ status: "deployed", displayName: "My Agent (draft)" });

const deployedRemoteCard = makeCard({ type: "external", status: "deployed", displayName: "My Remote Agent" });
const draftRemoteCard = makeCard({ type: "external", status: "deployed", displayName: "My Remote Agent (draft)" });

const deploymentV2 = (agentId: string, action: "deploy" | "update") =>
    HttpResponse.json({ data: { id: "dep-1", type: "agentDeployment", agentId, action, status: "success", failure: null, createdBy: "u", createdTime: null, updatedTime: null } }, { status: 201 });

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [agentToV2(userAgent)], meta: getPaginatedMeta(1) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [remoteAgentToV2(remoteUserAgent)], meta: getPaginatedMeta(1) })),
    http.get("*/api/v2/platform/toolsets", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({})),
    http.post("*/api/v2/platform/agentDeployments", () => deploymentV2(AGENT_ID, "update"))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderDialog(navigate = vi.fn()) {
    const onClose = vi.fn();
    renderWithProviders(<AgentCardComparisonDialog open={true} deployedAgent={deployedCard} draftAgent={draftCard} userAgent={userAgent} onClose={onClose} />, {
        chatContextValues: {
            navigate,
            addNotification: vi.fn(),
        },
    });
    return { onClose };
}

function renderRemoteDialog(navigate = vi.fn()) {
    const onClose = vi.fn();
    renderWithProviders(<AgentCardComparisonDialog open={true} deployedAgent={deployedRemoteCard} draftAgent={draftRemoteCard} userAgent={remoteUserAgent} onClose={onClose} />, {
        chatContextValues: {
            navigate,
            addNotification: vi.fn(),
        },
    });
    return { onClose };
}

describe("AgentCardComparisonDialog — Deploy Updates navigation", () => {
    test("Deploy Updates (user agent) → /agents?id=<id>&tab=deployed", async () => {
        const navigate = vi.fn();
        renderDialog(navigate);

        const deployBtn = await screen.findByTestId("deployUpdateButton");
        await userEvent.click(deployBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${AGENT_ID}&tab=deployed`);
        });
    });

    test("Deploy failure: navigate is NOT called when deploy errors", async () => {
        server.use(http.post("*/api/v2/platform/agentDeployments", () => HttpResponse.json({ message: "AWE unreachable", errorId: "e1" }, { status: 500 })));
        const navigate = vi.fn();
        renderDialog(navigate);

        const deployBtn = await screen.findByTestId("deployUpdateButton");
        await userEvent.click(deployBtn);

        await new Promise(r => setTimeout(r, 200));

        expect(navigate).not.toHaveBeenCalled();
    });
});

describe("AgentCardComparisonDialog — Deploy Updates (remote/external agent)", () => {
    test("Deploy Updates (remote agent) → /agents?id=<id>&tab=deployed", async () => {
        server.use(http.post("*/api/v2/platform/agentDeployments", () => deploymentV2(REMOTE_AGENT_ID, "deploy")));
        const navigate = vi.fn();
        renderRemoteDialog(navigate);

        const deployBtn = await screen.findByTestId("deployUpdateButton");
        await userEvent.click(deployBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${REMOTE_AGENT_ID}&tab=deployed`);
        });
    });

    test("Deploy failure (remote agent): navigate is NOT called when deploy errors", async () => {
        server.use(http.post("*/api/v2/platform/agentDeployments", () => HttpResponse.json({ message: "deployment failed", errorId: "e2" }, { status: 500 })));
        const navigate = vi.fn();
        renderRemoteDialog(navigate);

        const deployBtn = await screen.findByTestId("deployUpdateButton");
        await userEvent.click(deployBtn);

        await new Promise(r => setTimeout(r, 300));

        expect(navigate).not.toHaveBeenCalled();
    });
});
