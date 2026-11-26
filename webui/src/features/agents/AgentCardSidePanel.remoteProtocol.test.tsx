// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { transformRemoteAgent } from "@/api/remote-agents";

import { mockMessages, mockRemoteAgents } from "@/testing/data";
import { remoteAgentToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { RemoteAgent } from "@/types";

import { AgentCardSidePanel } from "./AgentCardSidePanel";

const REFUSED_TEXT = "The agent card failed its signature check, so no tasks are sent to this agent. Check the agent's card signature and the Trusted JWKS URLs. The card is checked again automatically.";

const refusedOnZeroThree = { selectedVersion: "v0_3", binding: "jsonrpc", deprecated: true, cardSignature: "refused" } as const;
const deployedSnapshot = {
    agentUrl: "https://orders.example.com",
    name: "PanelRemote",
    agentCardLocation: "well_known",
    agentAuthType: "none",
    requestTimeoutSeconds: 120,
    deployedAt: "2026-01-02T03:04:05.678Z",
    deployedBy: "alice@example.com",
};
const deployed: RemoteAgent = {
    ...mockRemoteAgents[0],
    id: "remote-protocol-sidepanel",
    name: "PanelRemote",
    agentCardLocation: "well_known",
    deploymentStatus: "deployed",
    runtimeStatus: { status: "running" },
    syncStatus: "in_sync",
    remoteProtocol: refusedOnZeroThree,
};

const server = setupServer(...withDefaults(http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json(paginatedResponse([remoteAgentToV2(deployed)])))));
beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderPanel(agent: RemoteAgent, selectedState: "active" | "inactive", viewOnly = false) {
    renderWithProviders(<AgentCardSidePanel agent={transformRemoteAgent(agent)} selectedState={selectedState} viewOnly={viewOnly} />, {
        chatContextValues: { messages: mockMessages, agents: [] },
    });
}

describe("AgentCardSidePanel — the A2A report of a remote agent", () => {
    test("shows the interface in use with its Older Version badge, and warns that the card was refused", async () => {
        renderPanel(deployed, "active");

        expect(await screen.findByTestId("sidePanelInterfaceInUse")).toHaveTextContent(/^A2A 0\.3 over JSON-RPCOlder Version$/);
        expect(screen.getByText(REFUSED_TEXT)).toBeInTheDocument();
    });

    test("keeps the interface but not the operator warning in view-only mode", async () => {
        renderPanel(deployed, "active", true);

        expect(await screen.findByTestId("sidePanelInterfaceInUse")).toHaveTextContent("A2A 0.3 over JSON-RPC");
        expect(screen.queryByText(REFUSED_TEXT)).toBeNull();
    });

    test("shows the deployed report on the deployed view of an out-of-sync agent, and not on its draft", async () => {
        const outOfSync: RemoteAgent = { ...deployed, syncStatus: "out_of_sync", deployed: deployedSnapshot };
        renderPanel(outOfSync, "active");
        expect(await screen.findByTestId("sidePanelInterfaceInUse")).toHaveTextContent("A2A 0.3 over JSON-RPC");
        expect(screen.getByText(REFUSED_TEXT)).toBeInTheDocument();
    });

    test("shows no report on the draft of an out-of-sync agent", async () => {
        const outOfSync: RemoteAgent = { ...deployed, syncStatus: "out_of_sync", deployed: deployedSnapshot };
        renderPanel(outOfSync, "inactive");
        await screen.findByText("Agent Details");
        expect(screen.queryByTestId("sidePanelInterfaceInUse")).toBeNull();
        expect(screen.queryByText(REFUSED_TEXT)).toBeNull();
    });

    test("shows no report for an undeployed agent that still carries a stale one", async () => {
        const undeployed: RemoteAgent = { ...deployed, deploymentStatus: "not_deployed", runtimeStatus: undefined, syncStatus: undefined };
        renderPanel(undeployed, "inactive");
        await screen.findByText("Agent Details");
        expect(screen.queryByTestId("sidePanelInterfaceInUse")).toBeNull();
        expect(screen.queryByText(REFUSED_TEXT)).toBeNull();
    });
});
