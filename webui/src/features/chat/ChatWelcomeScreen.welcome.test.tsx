// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { agentUIMode, FULL_UI_MODE } from "@/providers/contexts/UIModeContext";

import { ChatWelcomeScreen } from "@/features/chat/ChatWelcomeScreen";

import { mockDiscoveredAgentCardInfos } from "@/testing/data";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { AgentCardInfo } from "@/types";

vi.mock("./ChatInputArea", () => ({ ChatInputArea: () => <div data-testid="chat-input-stub" /> }));

const agentWithNoWelcome: AgentCardInfo = {
    ...mockDiscoveredAgentCardInfos[0],
    name: "PlainAgent",
    displayName: "PlainAgent",
    welcome: undefined,
};

function renderWelcome(configWelcomeMessage: string, uiMode: typeof FULL_UI_MODE) {
    return render(
        <StoryProvider chatContextValues={{ sessionId: "s1", selectedAgentName: "PlainAgent", messages: [] }} configContextValues={{ configWelcomeMessage }} uiModeContextValues={uiMode}>
            <ChatWelcomeScreen agents={[agentWithNoWelcome]} selectedAgentName="PlainAgent" />
        </StoryProvider>
    );
}

describe("ChatWelcomeScreen — instance-wide welcome message", () => {
    test("full mode: an agent with no card welcome falls through to the instance-wide configWelcomeMessage", async () => {
        renderWelcome("Welcome to Acme Assistant!", FULL_UI_MODE);
        expect(await screen.findByText("Welcome to Acme Assistant!")).toBeInTheDocument();
    });

    test("agent mode: the instance-wide configWelcomeMessage never appears, even though the agent has no card welcome", async () => {
        renderWelcome("Welcome to Acme Assistant!", agentUIMode("PlainAgent"));
        expect(await screen.findByText("What should we work on?")).toBeInTheDocument();
        expect(screen.queryByText("Welcome to Acme Assistant!")).not.toBeInTheDocument();
    });
});
