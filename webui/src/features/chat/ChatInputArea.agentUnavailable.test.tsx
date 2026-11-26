// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { ChatInputArea } from "@/features/chat/ChatInputArea";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { AgentCardInfo } from "@/types";

import { useSessionAgentAvailability } from "./hooks/useSessionAgentAvailability";

vi.mock("./hooks/useSessionAgentAvailability", () => ({ useSessionAgentAvailability: vi.fn() }));

beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn();
    Element.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
    Element.prototype.releasePointerCapture = vi.fn();
    Element.prototype.setPointerCapture = vi.fn();
});

const mockedAvailability = vi.mocked(useSessionAgentAvailability);

beforeEach(() => {
    mockedAvailability.mockReset();
});

const card = (name: string): AgentCardInfo => ({ name }) as AgentCardInfo;
const LIVE = card("live-agent");
const DELETED = "deleted-agent";
const UNAVAILABLE_MSG = "This chat's agent has been deleted. Select an agent to continue the conversation.";

function renderInput(chatContextValues = {}) {
    return render(
        <MemoryRouter>
            <StoryProvider chatContextValues={{ sessionId: "s1", selectedAgentName: DELETED, ...chatContextValues }}>
                <ChatInputArea agents={[LIVE]} />
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("ChatInputArea — session agent unavailable", () => {
    test("shows the banner and the placeholder (not the dead value), with send disabled", () => {
        mockedAvailability.mockReturnValue({ unavailable: true, bannerMessage: UNAVAILABLE_MSG });
        renderInput();

        expect(screen.getByText(UNAVAILABLE_MSG)).toBeInTheDocument();
        expect(screen.getByText("Select an agent...")).toBeInTheDocument();
        expect(screen.queryByText(DELETED)).not.toBeInTheDocument();
        expect(screen.getByTestId("sendMessage")).toBeDisabled();
    });

    test("picking an agent retargets the same session instead of starting a new chat", async () => {
        mockedAvailability.mockReturnValue({ unavailable: true, bannerMessage: UNAVAILABLE_MSG });
        const setSelectedAgentName = vi.fn();
        const handleNewSession = vi.fn();
        renderInput({ setSelectedAgentName, handleNewSession });

        await userEvent.click(screen.getByTestId("chatAgentSelect"));
        await userEvent.click(await screen.findByRole("option", { name: "live-agent" }));

        await waitFor(() => expect(setSelectedAgentName).toHaveBeenCalledWith("live-agent"));
        expect(handleNewSession).not.toHaveBeenCalled();
    });

    test("renders no banner when the agent is available", () => {
        mockedAvailability.mockReturnValue({ unavailable: false, bannerMessage: null });
        renderInput({ selectedAgentName: "live-agent" });
        expect(screen.queryByText(/Select an agent to continue the conversation/)).not.toBeInTheDocument();
    });

    test("keeps a minimum line height on the disabled composer so the placeholder isn't clipped", () => {
        mockedAvailability.mockReturnValue({ unavailable: true, bannerMessage: UNAVAILABLE_MSG });
        renderInput();
        const input = screen.getByTestId("chat-input");
        expect(input).toHaveAttribute("contenteditable", "false");
        expect(input).toHaveClass("min-h-12");
    });
});
