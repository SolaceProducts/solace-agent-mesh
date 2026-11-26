// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ChatProvider } from "./ChatProvider";
import { useChatContext } from "./contexts";

const DISPLAY_NAME_EXT = "https://solace.com/a2a/extensions/display-name";
const PRIORITY_EXT = "https://solace.com/a2a/extensions/sam/priority";
const WELCOME_EXT = "https://solace.com/a2a/extensions/sam/welcome";

const ACTIVITY_MONITOR_WIRE_NAME = "7c21-uuid";
const ORCHESTRATOR_WELCOME = "Orchestrator-only welcome text";

const card = (name: string, exts: { uri: string; params: Record<string, unknown> }[]) => ({
    name,
    description: "",
    url: "",
    version: "1",
    capabilities: { extensions: exts },
    skills: [],
    defaultInputModes: [],
    defaultOutputModes: [],
});

const cards = [
    card("Orchestrator", [
        { uri: PRIORITY_EXT, params: { priority: 10 } },
        { uri: WELCOME_EXT, params: { welcome_message: ORCHESTRATOR_WELCOME } },
    ]),
    card(ACTIVITY_MONITOR_WIRE_NAME, [{ uri: DISPLAY_NAME_EXT, params: { display_name: "Activity Monitor" } }]),
];

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: cards })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({}))
);

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function Consumer({ agentName }: { agentName?: string }) {
    const { selectedAgentName, messages, startNewChatWithPrompt } = useChatContext();
    const firstMessageText = messages[0]?.parts.find(part => part.kind === "text")?.text ?? "";
    return (
        <>
            <div data-testid="selected">{selectedAgentName}</div>
            <div data-testid="welcome">{firstMessageText}</div>
            <button data-testid="handoff" onClick={() => startNewChatWithPrompt({ promptText: "About the Feedback Dashboard: ", groupId: "ask-ai", groupName: "Ask AI", agentName })}>
                hand off
            </button>
        </>
    );
}

function renderChat(agentName?: string) {
    const router = createMemoryRouter(
        [
            {
                path: "*",
                element: (
                    <StoryProvider shouldIsolateQueryClient>
                        <ChatProvider disableBuilderBridge>
                            <Consumer agentName={agentName} />
                        </ChatProvider>
                    </StoryProvider>
                ),
            },
        ],
        { initialEntries: ["/chat"] }
    );
    render(<RouterProvider router={router} />);
}

describe("ChatProvider — a handoff that carries an agent", () => {
    test("selects the carried agent instead of the default", async () => {
        renderChat(ACTIVITY_MONITOR_WIRE_NAME);
        await waitFor(() => expect(screen.getByTestId("selected")).toHaveTextContent("Orchestrator"));
        await userEvent.click(screen.getByTestId("handoff"));

        await waitFor(() => expect(screen.getByTestId("selected")).toHaveTextContent(ACTIVITY_MONITOR_WIRE_NAME));
        expect(screen.getByTestId("welcome")).not.toHaveTextContent(ORCHESTRATOR_WELCOME);
    });

    test("falls back to the default pick when no agent is carried", async () => {
        renderChat(undefined);
        await waitFor(() => expect(screen.getByTestId("selected")).not.toHaveTextContent(""));

        await userEvent.click(screen.getByTestId("handoff"));

        await waitFor(() => expect(screen.getByTestId("selected")).toHaveTextContent("Orchestrator"));
        expect(screen.getByTestId("selected")).not.toHaveTextContent(ACTIVITY_MONITOR_WIRE_NAME);
    });

    test("does not fall back to the default when the carried agent is not in the card list", async () => {
        renderChat("agent-that-does-not-exist");
        await waitFor(() => expect(screen.getByTestId("selected")).toHaveTextContent("Orchestrator"));
        await userEvent.click(screen.getByTestId("handoff"));

        await new Promise(resolve => setTimeout(resolve, 50));
        expect(screen.getByTestId("selected")).toHaveTextContent("");
    });
});

function StuckCarryConsumer() {
    const { selectedAgentName, startNewChatWithPrompt, handleNewSession } = useChatContext();
    return (
        <>
            <div data-testid="selected">{selectedAgentName}</div>
            <button data-testid="handoff-with-unresolvable-agent" onClick={() => startNewChatWithPrompt({ promptText: "About the Feedback Dashboard: ", groupId: "ask-ai", groupName: "Ask AI", agentName: "agent-that-does-not-exist" })}>
                hand off with an agent that never resolves
            </button>
            <button data-testid="plain-new-chat" onClick={() => handleNewSession()}>
                new chat
            </button>
        </>
    );
}

describe("ChatProvider — pendingAgentName does not leak between handoffs", () => {
    test("a plain new chat ends on the default, not a previous handoff's stuck carry", async () => {
        const router = createMemoryRouter(
            [
                {
                    path: "*",
                    element: (
                        <StoryProvider shouldIsolateQueryClient>
                            <ChatProvider disableBuilderBridge>
                                <StuckCarryConsumer />
                            </ChatProvider>
                        </StoryProvider>
                    ),
                },
            ],
            { initialEntries: ["/chat"] }
        );
        render(<RouterProvider router={router} />);

        await waitFor(() => expect(screen.getByTestId("selected")).toHaveTextContent("Orchestrator"));

        await userEvent.click(screen.getByTestId("handoff-with-unresolvable-agent"));
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(screen.getByTestId("selected")).toHaveTextContent("");

        await userEvent.click(screen.getByTestId("plain-new-chat"));

        await waitFor(() => expect(screen.getByTestId("selected")).toHaveTextContent("Orchestrator"));
    });
});
