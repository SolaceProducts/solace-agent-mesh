// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

import { useConnectorsPaginated } from "@/features/connectors/hooks/useConnectorsPaginated";

import { remoteAgentToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { Agent, RemoteAgent } from "@/types";

import { ChatProvider } from "./ChatProvider";
import { useChatContext } from "./contexts";

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({}))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function Consumer({ agentId }: { agentId: string }) {
    const { handleDeleteAgent } = useChatContext();
    useConnectorsPaginated({ enabled: true, pageSize: 20 });
    return (
        <button data-testid="delete-agent" onClick={() => handleDeleteAgent({ id: agentId } as Agent)}>
            delete agent
        </button>
    );
}

function renderWithChatProvider(agentId = "agent-1", userContextValues: Partial<UserContextValue> = {}) {
    const router = createMemoryRouter(
        [
            {
                path: "*",
                element: (
                    <StoryProvider shouldIsolateQueryClient userContextValues={userContextValues}>
                        <ChatProvider disableBuilderBridge>
                            <Consumer agentId={agentId} />
                        </ChatProvider>
                    </StoryProvider>
                ),
            },
        ],
        { initialEntries: ["/chat"] }
    );
    render(<RouterProvider router={router} />);
}

describe("ChatProvider — handleDeleteAgent", () => {
    test("deletes via the user-agent endpoint and refreshes the connectors list", async () => {
        const connectorFetch = vi.fn(() => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) }));
        const userDelete = vi.fn(() => new HttpResponse(null, { status: 204 }));

        server.use(http.delete("*/api/v2/platform/agents/:agentId", userDelete), http.get("*/api/v2/platform/connectors", connectorFetch));

        renderWithChatProvider("agent-1");

        await waitFor(() => expect(connectorFetch).toHaveBeenCalled());
        const fetchesBefore = connectorFetch.mock.calls.length;

        await userEvent.click(screen.getByTestId("delete-agent"));

        await waitFor(() => expect(userDelete).toHaveBeenCalled());
        await waitFor(() => expect(connectorFetch.mock.calls.length).toBeGreaterThan(fetchesBefore));
    });

    test("deletes via the remote-agent endpoint and refreshes the connectors list", async () => {
        const remoteAgentsFetch = vi.fn(() => HttpResponse.json({ data: [remoteAgentToV2({ id: "remote-1" } as RemoteAgent)], meta: getPaginatedMeta(1) }));
        const remoteDelete = vi.fn(() => new HttpResponse(null, { status: 204 }));
        const userDelete = vi.fn(() => new HttpResponse(null, { status: 204 }));
        const connectorFetch = vi.fn(() => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) }));

        server.use(
            http.get("*/api/v2/platform/remoteAgents", remoteAgentsFetch),
            http.delete("*/api/v2/platform/remoteAgents/:agentId", remoteDelete),
            http.delete("*/api/v2/platform/agents/:agentId", userDelete),
            http.get("*/api/v2/platform/connectors", connectorFetch)
        );

        renderWithChatProvider("remote-1");

        await waitFor(() => expect(remoteAgentsFetch).toHaveBeenCalled());
        await waitFor(() => expect(connectorFetch).toHaveBeenCalled());
        const fetchesBefore = connectorFetch.mock.calls.length;

        await userEvent.click(screen.getByTestId("delete-agent"));

        await waitFor(() => expect(remoteDelete).toHaveBeenCalled());
        expect(userDelete).not.toHaveBeenCalled();
        await waitFor(() => expect(connectorFetch.mock.calls.length).toBeGreaterThan(fetchesBefore));
    });

    test("does not refresh the connectors list when the delete request fails", async () => {
        const connectorFetch = vi.fn(() => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) }));
        const userDelete = vi.fn(() => new HttpResponse(null, { status: 500 }));

        server.use(http.delete("*/api/v2/platform/agents/:agentId", userDelete), http.get("*/api/v2/platform/connectors", connectorFetch));

        renderWithChatProvider("agent-1");

        await waitFor(() => expect(connectorFetch).toHaveBeenCalled());
        const fetchesBefore = connectorFetch.mock.calls.length;

        await userEvent.click(screen.getByTestId("delete-agent"));

        await waitFor(() => expect(userDelete).toHaveBeenCalled());
        expect(connectorFetch.mock.calls.length).toBe(fetchesBefore);
    });
});
