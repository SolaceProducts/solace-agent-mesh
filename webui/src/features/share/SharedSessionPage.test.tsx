// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

const mockUseSharedSession = vi.fn();

describe("SharedSessionPage", () => {
    let SharedSessionPage: React.ComponentType;

    beforeEach(async () => {
        vi.resetModules();
        mockUseSharedSession.mockReset();

        vi.doMock("@/features/share/hooks/useSharedSession", () => ({
            useSharedSession: mockUseSharedSession,
            formatDateYMD: (epochMs: number) => new Date(epochMs).toISOString().slice(0, 10),
        }));

        vi.doMock("@/features/chat", () => ({
            ChatMessage: () => React.createElement("div", { "data-testid": "chat-message" }),
        }));

        vi.doMock("@/features/share/SharedSidePanel", () => ({
            SharedSidePanel: () => React.createElement("div", { "data-testid": "side-panel" }),
        }));

        vi.doMock("@/providers/SharedChatProvider", () => ({
            SharedChatProvider: ({ children }: { children: React.ReactNode }) => React.createElement("div", null, children),
        }));

        const mod = await import("@/features/share/SharedSessionPage");
        SharedSessionPage = mod.SharedSessionPage;
    });

    function renderPage() {
        const queryClient = new QueryClient({
            defaultOptions: { queries: { retry: false } },
        });
        return render(React.createElement(QueryClientProvider, { client: queryClient }, React.createElement(MemoryRouter, null, React.createElement(SharedSessionPage))));
    }

    test("shows spinner when loading", () => {
        mockUseSharedSession.mockReturnValue({
            loading: true,
            error: null,
            session: null,
        });

        renderPage();
        expect(screen.getByText("Loading shared session...")).toBeInTheDocument();
    });

    test("shows error message when there is an error", () => {
        mockUseSharedSession.mockReturnValue({
            loading: false,
            error: "Session access denied",
            session: null,
            navigate: vi.fn(),
        });

        renderPage();
        expect(screen.getByText("Unable to View Shared Session")).toBeInTheDocument();
        expect(screen.getByText("Session access denied")).toBeInTheDocument();
    });

    test("shows not found state when session is null without error", () => {
        mockUseSharedSession.mockReturnValue({
            loading: false,
            error: null,
            session: null,
            navigate: vi.fn(),
        });

        renderPage();
        expect(screen.getByText("Shared Session Not Found")).toBeInTheDocument();
    });

    function makeLoadedSession(overrides: Record<string, unknown> = {}) {
        return {
            loading: false,
            error: null,
            session: {
                title: "My Shared Session",
                createdAt: "2023-11-14T22:13:20.000Z",
                snapshotTime: null,
                tasks: [{ userId: "alice@example.com" }],
            },
            messages: [
                {
                    isUser: true,
                    parts: [{ kind: "text", text: "Hello" }],
                    taskId: "task-1",
                    metadata: { messageId: "msg-1" },
                },
                {
                    isUser: false,
                    parts: [{ kind: "text", text: "Hi there" }],
                    taskId: "task-1",
                    metadata: { messageId: "msg-2" },
                },
            ],
            lastMessageIndexByTaskId: new Map([["task-1", 1]]),
            navigate: vi.fn(),
            shareId: "share-123",
            sessionIdForProvider: "session-456",
            convertedArtifacts: [],
            ragData: {},
            handleProviderTabOpen: vi.fn(),
            setSelectedTaskId: vi.fn(),
            selectedTaskId: null,
            isSidePanelCollapsed: false,
            activeSidePanelTab: "files",
            setActiveSidePanelTab: vi.fn(),
            toggleSidePanel: vi.fn(),
            openSidePanelTab: vi.fn(),
            hasRagSources: false,
            handleSharedArtifactDownload: vi.fn(),
            handleForkChat: vi.fn(),
            isForking: false,
            ...overrides,
        };
    }

    test("renders session title when loaded", () => {
        mockUseSharedSession.mockReturnValue(makeLoadedSession());

        renderPage();
        expect(screen.getByText("My Shared Session")).toBeInTheDocument();
    });

    test("fork button click triggers handleForkChat", async () => {
        const handleForkChat = vi.fn();
        mockUseSharedSession.mockReturnValue(makeLoadedSession({ handleForkChat }));

        renderPage();
        const forkButton = screen.getByRole("button", { name: /Continue in New Chat/i });
        forkButton.click();
        expect(handleForkChat).toHaveBeenCalledTimes(1);
    });

    test("read-only banner is shown", () => {
        mockUseSharedSession.mockReturnValue(makeLoadedSession());

        renderPage();
        expect(screen.getByText("This chat is read-only. To build off of it, continue a new conversation.")).toBeInTheDocument();
    });

    test("shows shared-by info when no snapshot time", () => {
        mockUseSharedSession.mockReturnValue(makeLoadedSession());

        renderPage();
        expect(screen.getByText("alice@example.com")).toBeInTheDocument();
    });

    test("shows snapshot date when snapshotTime is present", () => {
        const session = makeLoadedSession();
        (session.session as Record<string, unknown>).snapshotTime = 1700000000000;
        mockUseSharedSession.mockReturnValue(session);

        renderPage();
        expect(screen.getByText(/Snapshot from/)).toBeInTheDocument();
    });

    test("shows empty state when there are no messages", () => {
        mockUseSharedSession.mockReturnValue(makeLoadedSession({ messages: [] }));

        renderPage();
        expect(screen.getByText("No messages in this session.")).toBeInTheDocument();
    });
});
