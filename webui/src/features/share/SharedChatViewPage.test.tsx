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
const markShareViewed = vi.fn();

async function loadPage() {
    vi.resetModules();

    vi.doMock("@/features/share/hooks/useSharedSession", () => ({
        useSharedSession: mockUseSharedSession,
        formatDateYMD: (epochMs: number) => new Date(epochMs).toISOString().slice(0, 10),
    }));

    vi.doMock("@/api/share", () => ({
        useMarkShareViewed: () => ({ mutate: markShareViewed }),
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

    vi.doMock("@/common/layout/Header", () => ({
        Header: ({ title }: { title: string }) => React.createElement("header", { "data-testid": "header" }, title),
    }));

    const mod = await import("@/features/share/SharedChatViewPage");
    return mod.SharedChatViewPage;
}

describe("SharedChatViewPage", () => {
    let SharedChatViewPage: React.ComponentType;

    beforeEach(async () => {
        mockUseSharedSession.mockReset();
        markShareViewed.mockReset();
        SharedChatViewPage = await loadPage();
    });

    function renderPage() {
        const queryClient = new QueryClient({
            defaultOptions: { queries: { retry: false } },
        });
        return render(React.createElement(QueryClientProvider, { client: queryClient }, React.createElement(MemoryRouter, null, React.createElement(SharedChatViewPage))));
    }

    test("shows spinner when loading", () => {
        mockUseSharedSession.mockReturnValue({
            loading: true,
            error: null,
            session: null,
        });

        renderPage();
        expect(screen.getByText("Loading shared chat...")).toBeInTheDocument();
    });

    test("shows error message when there is an error", () => {
        mockUseSharedSession.mockReturnValue({
            loading: false,
            error: "Access denied",
            session: null,
            navigate: vi.fn(),
        });

        renderPage();
        expect(screen.getByText("Unable to View Shared Chat")).toBeInTheDocument();
        expect(screen.getByText("Access denied")).toBeInTheDocument();
    });

    test("shows not found state when session is null without error", () => {
        mockUseSharedSession.mockReturnValue({
            loading: false,
            error: null,
            session: null,
            navigate: vi.fn(),
        });

        renderPage();
        expect(screen.getByText("Shared Chat Not Found")).toBeInTheDocument();
    });

    describe("marking the share viewed", () => {
        const loadedSession = (shareId: string) => ({
            loading: false,
            error: null,
            shareId,
            session: { title: "Q3 planning", tasks: [], createdAt: 0, isOwner: false, sessionId: null },
            messages: [],
            convertedArtifacts: [],
            ragData: null,
            sessionIdForProvider: "sess-1",
            lastMessageIndexByTaskId: new Map(),
            isSidePanelCollapsed: true,
            handleProviderTabOpen: vi.fn(),
            setSelectedTaskId: vi.fn(),
            handleForkChat: vi.fn(),
            isForking: false,
            navigate: vi.fn(),
        });

        test("marks the share viewed once the share has loaded", () => {
            mockUseSharedSession.mockReturnValue(loadedSession("share-abc"));

            renderPage();

            expect(markShareViewed).toHaveBeenCalledTimes(1);
            expect(markShareViewed).toHaveBeenCalledWith("share-abc");
        });

        test("does not mark viewed while still loading", () => {
            mockUseSharedSession.mockReturnValue({ loading: true, error: null, session: null, shareId: "share-abc", navigate: vi.fn() });

            renderPage();

            expect(markShareViewed).not.toHaveBeenCalled();
        });

        test("does not mark viewed when the share errored", () => {
            mockUseSharedSession.mockReturnValue({ loading: false, error: "Access denied", session: null, shareId: "share-abc", navigate: vi.fn() });

            renderPage();

            expect(markShareViewed).not.toHaveBeenCalled();
        });

        test("does not re-post when the page re-renders for the same share", () => {
            mockUseSharedSession.mockReturnValue(loadedSession("share-abc"));

            const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
            const tree = () => React.createElement(QueryClientProvider, { client: queryClient }, React.createElement(MemoryRouter, null, React.createElement(SharedChatViewPage)));
            const { rerender } = render(tree());
            rerender(tree());

            expect(markShareViewed).toHaveBeenCalledTimes(1);
        });
    });
});
