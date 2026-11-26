// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { ChatWelcomeScreen } from "@/features/chat/ChatWelcomeScreen";

import { mockDiscoveredAgentCardInfos } from "@/testing/data";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { AgentCardInfo, AgentWelcomeConfig } from "@/types";

vi.mock("./ChatInputArea", () => ({ ChatInputArea: () => <div data-testid="chat-input-stub" /> }));

const welcome: AgentWelcomeConfig = {
    welcome_message: "Welcome to the Weather Expert!",
    suggestions: [
        { label: "Forecast", prompt: "What's the forecast for " },
        { label: "Compare", prompt: "Compare the weather between " },
        { label: "Today", prompt: "Give me today's weather summary", auto_send: true },
    ],
};

const agentWith = (w?: AgentWelcomeConfig): AgentCardInfo => ({
    ...mockDiscoveredAgentCardInfos[0],
    name: "WeatherAgent",
    displayName: "WeatherAgent",
    welcome: w,
});

function renderWelcome(agents: AgentCardInfo[]) {
    return render(
        <StoryProvider chatContextValues={{ sessionId: "s1", selectedAgentName: "WeatherAgent", messages: [] }}>
            <ChatWelcomeScreen agents={agents} selectedAgentName="WeatherAgent" />
        </StoryProvider>
    );
}

describe("ChatWelcomeScreen — suggestion pill focus traversal", () => {
    test("Tab advances through the pills in order", async () => {
        const user = userEvent.setup();
        renderWelcome([agentWith(welcome)]);

        const forecast = await screen.findByRole("button", { name: /Forecast/ });
        const compare = await screen.findByRole("button", { name: /Compare/ });

        forecast.focus();
        expect(forecast).toHaveFocus();

        await user.tab();
        expect(compare).toHaveFocus();
    });

    test("keeps the agent suggestions and focus when the card momentarily loses its welcome", async () => {
        const { rerender } = renderWelcome([agentWith(welcome)]);

        const compare = await screen.findByRole("button", { name: /Compare/ });
        compare.focus();
        expect(compare).toHaveFocus();

        rerender(
            <StoryProvider chatContextValues={{ sessionId: "s1", selectedAgentName: "WeatherAgent", messages: [] }}>
                <ChatWelcomeScreen agents={[agentWith(undefined)]} selectedAgentName="WeatherAgent" />
            </StoryProvider>
        );

        expect(screen.getByRole("button", { name: /Compare/ })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Brainstorm/ })).toBeNull();
        expect(screen.getByRole("button", { name: /Compare/ })).toHaveFocus();
    });

    test("keeps focus when the card is re-minted with identical welcome data", async () => {
        const { rerender } = renderWelcome([agentWith(welcome)]);

        const compare = await screen.findByRole("button", { name: /Compare/ });
        compare.focus();
        expect(compare).toHaveFocus();

        rerender(
            <StoryProvider chatContextValues={{ sessionId: "s1", selectedAgentName: "WeatherAgent", messages: [] }}>
                <ChatWelcomeScreen agents={[agentWith({ ...welcome, suggestions: welcome.suggestions!.map(s => ({ ...s })) })]} selectedAgentName="WeatherAgent" />
            </StoryProvider>
        );

        expect(screen.getByRole("button", { name: /Compare/ })).toHaveFocus();
    });
});
