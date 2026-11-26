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

import { mockRemoteAgents } from "@/testing/data";
import { remoteAgentToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta } from "@/testing/helpers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { RemoteAgent } from "@/types";

import { RemoteAgentViewPage } from "./RemoteAgentViewPage";

expect.extend(matchers);

const undeployedAgent = mockRemoteAgents[3];

const deployedAgent = mockRemoteAgents[0];

const outOfSyncAgent = mockRemoteAgents[10];

const deploymentV2 = (agentId: string, action: "deploy" | "undeploy") =>
    HttpResponse.json({ data: { id: "dep-1", type: "agentDeployment", agentId, action, status: "success", failure: null, createdBy: "u", createdTime: null, updatedTime: null } }, { status: 201 });

const server = setupServer(
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: mockRemoteAgents.map(remoteAgentToV2), meta: getPaginatedMeta(mockRemoteAgents.length) })),
    http.get("*/api/v2/platform/remoteAgents/:id", ({ params }) => {
        const agent = mockRemoteAgents.find(a => a.id === params["id"]);
        return agent ? HttpResponse.json({ data: remoteAgentToV2(agent) }) : HttpResponse.json({ message: "not found", errorId: "e" }, { status: 404 });
    }),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({})),
    http.post("*/api/v2/platform/agentDeployments", () => deploymentV2(undeployedAgent.id!, "deploy"))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderPage(remoteAgentId: string, navigate = vi.fn()) {
    return renderWithProviders(<RemoteAgentViewPage />, {
        chatContextValues: {
            navigate,
            useLoaderData: (() => ({ remoteAgentId })) as unknown as typeof import("react-router-dom").useLoaderData,
            handleDeleteAgent: vi.fn(async () => true),
            addNotification: vi.fn(),
        },
    });
}

describe("RemoteAgentViewPage — agent not found", () => {
    test("shows error empty state when agent ID does not exist in list", async () => {
        server.use(http.get("*/api/v2/platform/remoteAgents/:id", () => HttpResponse.json({ message: "not found", errorId: "e" }, { status: 404 })));
        const navigate = vi.fn();
        renderWithProviders(<RemoteAgentViewPage />, {
            chatContextValues: {
                navigate,
                useLoaderData: (() => ({ remoteAgentId: "does-not-exist" })) as unknown as typeof import("react-router-dom").useLoaderData,
                handleDeleteAgent: vi.fn(async () => true),
                addNotification: vi.fn(),
            },
        });

        await screen.findByText("Remote Agent Not Found");

        await userEvent.click(screen.getByRole("button", { name: "Go To Agents" }));
        expect(navigate).toHaveBeenCalledWith("/agents");
    });
});

describe("RemoteAgentViewPage — Close button navigation", () => {
    test("not_deployed agent → /agents?id=<id>&tab=undeployed", async () => {
        const navigate = vi.fn();
        renderPage(undeployedAgent.id!, navigate);

        const btn = await screen.findByTestId("closeButton");
        await userEvent.click(btn);

        expect(navigate).toHaveBeenCalledWith(`/agents?id=${undeployedAgent.id}&tab=undeployed`);
    });

    test("deployed agent → /agents?id=<id>&tab=deployed", async () => {
        const navigate = vi.fn();
        renderPage(deployedAgent.id!, navigate);

        const btn = await screen.findByTestId("closeButton");
        await userEvent.click(btn);

        expect(navigate).toHaveBeenCalledWith(`/agents?id=${deployedAgent.id}&tab=deployed`);
    });

    test("the Agents breadcrumb leads where Close does", async () => {
        const navigate = vi.fn();
        renderPage(undeployedAgent.id!, navigate);

        await userEvent.click(await screen.findByRole("button", { name: "Agents" }));

        expect(navigate).toHaveBeenCalledWith(`/agents?id=${undeployedAgent.id}&tab=undeployed`);
    });

    test("unknown agent ID (no match in list) → /agents", async () => {
        server.use(
            http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
            http.get("*/api/v2/platform/remoteAgents/:id", () => HttpResponse.json({ data: remoteAgentToV2(undeployedAgent) }))
        );
        const navigate = vi.fn();
        renderPage(undeployedAgent.id!, navigate);

        await screen.findByTestId("closeButton");
        const btn = screen.getByTestId("closeButton");
        await userEvent.click(btn);

        expect(navigate).toHaveBeenCalledWith("/agents");
    });
});

describe("RemoteAgentViewPage — Edit navigation", () => {
    test("Edit → /agents/external/<id>/edit", async () => {
        const navigate = vi.fn();
        renderPage(undeployedAgent.id!, navigate);

        const editBtn = await screen.findByTestId("editRemoteAgentButton");
        await userEvent.click(editBtn);

        expect(navigate).toHaveBeenCalledWith(`/agents/external/${undeployedAgent.id}/edit`);
    });
});

describe("RemoteAgentViewPage — deployed-but-not-running agent", () => {
    const notRunningAgent = {
        ...deployedAgent,
        id: "not-running-remote-1",
        name: "NotRunningRemote",
        deploymentStatus: "deployed" as const,
        runtimeStatus: { status: "disconnected" as const },
        syncStatus: "in_sync" as const,
    };

    test("offers Undeploy (not Redeploy/Delete); confirming Undeploy navigates to tab=undeployed", async () => {
        server.use(
            http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [remoteAgentToV2(notRunningAgent)], meta: getPaginatedMeta(1) })),
            http.get("*/api/v2/platform/remoteAgents/:id", () => HttpResponse.json({ data: remoteAgentToV2(notRunningAgent) })),
            http.post("*/api/v2/platform/agentDeployments", () => deploymentV2(notRunningAgent.id, "undeploy"))
        );
        const navigate = vi.fn();
        renderPage(notRunningAgent.id, navigate);

        const undeployBtn = await screen.findByTestId("undeployRemoteAgentButton");
        expect(undeployBtn).toBeInTheDocument();
        expect(screen.queryByTestId("moreActionsButton")).not.toBeInTheDocument();

        await userEvent.click(undeployBtn);

        const confirmBtn = await screen.findByRole("button", { name: /^Undeploy$/i });
        await userEvent.click(confirmBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${notRunningAgent.id}&tab=undeployed`);
        });
    });

    test("Close navigates to tab=deployed (the agent's deployment intent)", async () => {
        server.use(
            http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [remoteAgentToV2(notRunningAgent)], meta: getPaginatedMeta(1) })),
            http.get("*/api/v2/platform/remoteAgents/:id", () => HttpResponse.json({ data: remoteAgentToV2(notRunningAgent) }))
        );
        const navigate = vi.fn();
        renderPage(notRunningAgent.id, navigate);

        const btn = await screen.findByTestId("closeButton");
        await userEvent.click(btn);

        expect(navigate).toHaveBeenCalledWith(`/agents?id=${notRunningAgent.id}&tab=deployed`);
    });
});

describe("RemoteAgentViewPage — Deploy dialog confirmation", () => {
    test("confirming Deploy navigates to tab=deployed for not_deployed agent", async () => {
        const navigate = vi.fn();
        renderPage(undeployedAgent.id!, navigate);

        const deployBtn = await screen.findByTestId("deployRemoteAgentButton");
        await userEvent.click(deployBtn);

        const confirmBtn = await screen.findByRole("button", { name: /^Deploy$/i });
        await userEvent.click(confirmBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${undeployedAgent.id}&tab=deployed`);
        });
    });

    test("Deploy failure keeps dialog open and does not navigate", async () => {
        server.use(http.post("*/api/v2/platform/agentDeployments", () => HttpResponse.json({ message: "Internal server error", errorId: "e" }, { status: 500 })));
        const navigate = vi.fn();
        renderPage(undeployedAgent.id!, navigate);

        const deployBtn = await screen.findByTestId("deployRemoteAgentButton");
        await userEvent.click(deployBtn);

        const confirmBtn = await screen.findByRole("button", { name: /^Deploy$/i });
        await userEvent.click(confirmBtn);

        await waitFor(() => {
            expect(navigate).not.toHaveBeenCalled();
        });
    });

    test("out_of_sync deployed agent: confirming Deploy Updates navigates to tab=deployed", async () => {
        const navigate = vi.fn();
        server.use(http.post("*/api/v2/platform/agentDeployments", () => deploymentV2(outOfSyncAgent.id!, "deploy")));
        renderPage(outOfSyncAgent.id!, navigate);

        const deployBtn = await screen.findByTestId("deployRemoteAgentButton");
        expect(deployBtn).toHaveTextContent("Deploy Updates");
        await userEvent.click(deployBtn);

        const confirmBtn = await screen.findByRole("button", { name: /^Deploy$/i });
        await userEvent.click(confirmBtn);

        await waitFor(() => {
            expect(navigate).toHaveBeenCalledWith(`/agents?id=${outOfSyncAgent.id}&tab=deployed`);
        });
    });
});

describe("RemoteAgentViewPage — A2A protocol the deployed agent reports", () => {
    const protocolAgent: RemoteAgent = {
        ...deployedAgent,
        id: "protocol-remote-1",
        name: "ProtocolRemote",
        agentCardLocation: "well_known",
        protocolVersion: "auto",
        trustedCardJwksUrls: ["https://keys.example.com/jwks.json"],
        requireSignedCard: true,
        deploymentStatus: "deployed",
        runtimeStatus: { status: "running" },
        syncStatus: "in_sync",
    };
    const verifiedOneZero = { selectedVersion: "v1_0", binding: "jsonrpc", deprecated: false, cardSignature: "verified" } as const;
    const zeroThree = { selectedVersion: "v0_3", binding: "jsonrpc", deprecated: true, cardSignature: "unchecked" } as const;

    function serve(listEntry: RemoteAgent, single: RemoteAgent) {
        server.use(
            http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [remoteAgentToV2(listEntry)], meta: getPaginatedMeta(1) })),
            http.get("*/api/v2/platform/remoteAgents/:id", () => HttpResponse.json({ data: remoteAgentToV2(single) }))
        );
    }

    const REFUSED_TEXT = "The agent card failed its signature check, so no tasks are sent to this agent. Check the agent's card signature and the Trusted JWKS URLs. The card is checked again automatically.";
    const statusRow = () => screen.getByText("Status:").parentElement?.textContent;

    test("a report the single GET carries for an agent it says is undeployed shows nowhere", async () => {
        serve(protocolAgent, { ...protocolAgent, deploymentStatus: "not_deployed", runtimeStatus: undefined, remoteProtocol: zeroThree });
        renderPage(protocolAgent.id);

        expect(await screen.findByTestId("remoteProtocolInUse")).toHaveTextContent("Not reported yet");
        expect(statusRow()).toBe("Status:Running");
        expect(screen.queryByText(/older version/i)).toBeNull();
    });

    test("shows the single GET's state, not an older report the list entry carries", async () => {
        serve({ ...protocolAgent, remoteProtocol: verifiedOneZero }, protocolAgent);
        renderPage(protocolAgent.id);

        expect(await screen.findByTestId("remoteProtocolInUse")).toHaveTextContent("Not reported yet");
        expect(statusRow()).toBe("Status:Running");
    });

    test("shows a verified report in the A2A Protocol section, with no version or signature badge in the status line", async () => {
        serve(protocolAgent, { ...protocolAgent, remoteProtocol: verifiedOneZero });
        renderPage(protocolAgent.id);

        expect(await screen.findByTestId("remoteProtocolInUse")).toHaveTextContent(/^A2A 1\.0 over JSON-RPC$/);
        expect(statusRow()).toBe("Status:Running");
        expect(screen.getByTestId("remoteProtocolCardSignature")).toHaveTextContent(/^Verified$/);
    });

    test("A2A 0.3 the list entry has not seen yet shows in Interface In Use with its badge, and not in the status line", async () => {
        serve(protocolAgent, { ...protocolAgent, remoteProtocol: zeroThree });
        renderPage(protocolAgent.id);

        expect(await screen.findByTestId("remoteProtocolInUse")).toHaveTextContent(/^A2A 0\.3 over JSON-RPCOlder Version$/);
        expect(statusRow()).toBe("Status:Running");
        expect(screen.getAllByText("Older Version")).toHaveLength(1);
    });

    test("a refused card shows a warning above the agent's details", async () => {
        serve(protocolAgent, { ...protocolAgent, remoteProtocol: { ...verifiedOneZero, cardSignature: "refused" } });
        renderPage(protocolAgent.id);

        expect(await screen.findByText(REFUSED_TEXT)).toBeInTheDocument();
        expect(screen.getByTestId("remoteProtocolCardSignature")).toHaveTextContent("Refused: tasks are not sent to the agent");
    });

    test("a verified card shows no refusal warning", async () => {
        serve(protocolAgent, { ...protocolAgent, remoteProtocol: verifiedOneZero });
        renderPage(protocolAgent.id);

        await screen.findByTestId("remoteProtocolInUse");
        expect(screen.queryByText(REFUSED_TEXT)).toBeNull();
    });

    test("after a redeploy moves the agent off 0.3, a list entry still on 0.3 shows nowhere", async () => {
        serve({ ...protocolAgent, remoteProtocol: zeroThree }, { ...protocolAgent, remoteProtocol: verifiedOneZero });
        renderPage(protocolAgent.id);

        expect(await screen.findByTestId("remoteProtocolInUse")).toHaveTextContent(/^A2A 1\.0 over JSON-RPC$/);
        expect(statusRow()).toBe("Status:Running");
        expect(screen.queryByText(/older version/i)).toBeNull();
    });

    test("an agent the list says is not deployed shows no report, whatever a stale report or the single GET says", async () => {
        const notDeployed: RemoteAgent = { ...protocolAgent, deploymentStatus: "not_deployed", runtimeStatus: undefined, remoteProtocol: zeroThree };
        serve(notDeployed, { ...protocolAgent, remoteProtocol: { ...zeroThree, cardSignature: "refused" } });
        renderPage(protocolAgent.id);

        await screen.findByTestId("deployRemoteAgentButton");
        expect(statusRow()).toBe("Status:Not Deployed");
        expect(screen.getByText("A2A Protocol")).toBeInTheDocument();
        expect(screen.queryByTestId("remoteProtocolInUse")).toBeNull();
        expect(screen.queryByTestId("remoteProtocolCardSignature")).toBeNull();
        expect(screen.queryByText(REFUSED_TEXT)).toBeNull();
    });

    test("an out-of-sync agent on A2A 0.3 still shows what is deployed in Interface In Use", async () => {
        const deployedSnapshot = {
            agentUrl: protocolAgent.agentUrl,
            name: protocolAgent.name,
            agentCardLocation: "well_known",
            agentAuthType: "none",
            requestTimeoutSeconds: 120,
            deployedAt: "2026-01-02T03:04:05.678Z",
            deployedBy: "alice@example.com",
        };
        const outOfSync: RemoteAgent = { ...protocolAgent, syncStatus: "out_of_sync", deployed: deployedSnapshot, remoteProtocol: zeroThree };
        serve(outOfSync, outOfSync);
        renderPage(protocolAgent.id);

        await waitFor(() => expect(screen.getByTestId("remoteProtocolInUse")).toHaveTextContent(/^A2A 0\.3 over JSON-RPCOlder Version$/));
    });

    test("an out-of-sync agent with no deployed snapshot still shows the version it uses in Interface In Use", async () => {
        const outOfSync: RemoteAgent = { ...protocolAgent, syncStatus: "out_of_sync", deployed: undefined };
        serve(outOfSync, { ...outOfSync, remoteProtocol: zeroThree });
        renderPage(protocolAgent.id);

        expect(await screen.findByTestId("remoteProtocolInUse")).toHaveTextContent(/^A2A 0\.3 over JSON-RPCOlder Version$/);
    });

    test("shows the stored A2A version pin", async () => {
        const pinned: RemoteAgent = { ...protocolAgent, protocolVersion: "v0_3" };
        serve(pinned, pinned);
        renderPage(protocolAgent.id);

        const label = await screen.findByText("A2A Version");
        expect(label.nextElementSibling).toHaveTextContent(/^A2A 0\.3$/);
    });

    test("a card-less agent shows no card trust rows", async () => {
        const cardless: RemoteAgent = { ...protocolAgent, agentCardLocation: "none", trustedCardJwksUrls: [], requireSignedCard: false, remoteProtocol: verifiedOneZero };
        serve(cardless, cardless);
        renderPage(protocolAgent.id);

        await screen.findByTestId("remoteProtocolInUse");
        expect(screen.queryByText("Trusted JWKS URLs")).toBeNull();
        expect(screen.queryByText("Require Signed Card")).toBeNull();
        expect(screen.queryByTestId("remoteProtocolCardSignature")).toBeNull();
    });
});
