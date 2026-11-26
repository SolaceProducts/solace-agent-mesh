// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { mockDiscoveredAgentCardInfos } from "@/testing/data";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { AgentCardInfo } from "@/types";

import { ChatWelcomeScreen } from "./ChatWelcomeScreen";

vi.mock("./ChatInputArea", () => ({ ChatInputArea: () => <div data-testid="chat-input-stub" /> }));

const agentWithNoWelcome: AgentCardInfo = {
    ...mockDiscoveredAgentCardInfos[0],
    name: "PlainAgent",
    displayName: "PlainAgent",
    welcome: undefined,
};

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});
afterEach(() => {
    vi.restoreAllMocks();
});

function renderWelcome(welcomeOverride?: AgentCardInfo["welcome"]) {
    return render(
        <StoryProvider chatContextValues={{ sessionId: "s1", selectedAgentName: "PlainAgent", messages: [] }}>
            <ChatWelcomeScreen agents={[agentWithNoWelcome]} selectedAgentName="PlainAgent" welcomeOverride={welcomeOverride} />
        </StoryProvider>
    );
}

describe("chat:suggestion_used", () => {
    test.each([
        ["Write", "write"],
        ["Learn", "learn"],
        ["Code", "code"],
        ["Brainstorm", "brainstorm"],
        ["Surprise me", "surprise_me"],
    ])("clicking the %s chip reports suggestionLabel %s", async (label, suggestionLabel) => {
        renderWelcome();
        await userEvent.click(screen.getByText(label));

        expect(trackSpy).toHaveBeenCalledWith("chat:suggestion_used", { suggestionLabel });
    });

    test("clicking an agent-provided suggestion reports agent_custom, never its own label", async () => {
        renderWelcome({ suggestions: [{ label: "Do something amazing", prompt: "Do something amazing" }] });
        await userEvent.click(screen.getByText("Do something amazing"));

        expect(trackSpy).toHaveBeenCalledWith("chat:suggestion_used", { suggestionLabel: "agent_custom" });
    });
});
