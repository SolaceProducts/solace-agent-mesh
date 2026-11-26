// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

type RecentResult = {
    data: Array<{ id: string; name: string; updatedAt: string }> | undefined;
    isLoading: boolean;
    isFetching: boolean;
};

let recentResult: RecentResult = { data: undefined, isLoading: true, isFetching: true };

async function loadList() {
    vi.resetModules();

    vi.doMock("@/api/sessions", () => ({
        useRecentSessions: () => recentResult,
        useMarkSessionViewed: () => ({ mutate: vi.fn() }),
        useRenameSessionWithAI: () => ({ mutate: vi.fn(), isPending: false }),
    }));

    vi.doMock("@/api/share", () => ({
        useSharedWithMe: () => ({ data: [], isLoading: false }),
        useMarkShareViewed: () => ({ mutate: vi.fn() }),
    }));

    vi.doMock("@/providers/contexts/ChatContext", () => ({
        useChatContext: () => ({ sessionId: "current", handleSwitchSession: vi.fn(), currentTaskId: null, updateSessionName: vi.fn(), openSessionDeleteModal: vi.fn(), addNotification: vi.fn() }),
    }));

    vi.doMock("@/providers/contexts/ConfigContext", () => ({
        useConfigContext: () => ({ persistenceEnabled: true }),
    }));

    vi.doMock("@/providers/contexts/UIModeContext", async () => {
        const actual = await vi.importActual<typeof import("@/providers/contexts/UIModeContext")>("@/providers/contexts/UIModeContext");
        return {
            ...actual,
            useUIModeContext: () => ({ allowedSessionActions: [] }),
        };
    });

    vi.doMock("@/hooks", async () => {
        const actual = await vi.importActual<typeof import("@/hooks")>("@/hooks");
        return {
            ...actual,
            useIsAutoTitleGenerationEnabled: () => false,
            useIsChatSharingEnabled: () => false,
            useBuilderEnabled: () => true,
            useTitleAnimation: (name: string) => ({ text: name, isAnimating: false, isGenerating: false }),
        };
    });

    const mod = await import("@/features/chat/recentChats/RecentChatsList");
    return mod.RecentChatsList;
}

async function renderList() {
    const RecentChatsList = await loadList();
    return render(
        <MemoryRouter>
            <RecentChatsList />
        </MemoryRouter>
    );
}

describe("RecentChatsList — recentNotReady spinner gate", () => {
    beforeEach(() => {
        recentResult = { data: undefined, isLoading: true, isFetching: true };
    });

    test("first paint with user A renders user A's titles", async () => {
        recentResult = {
            data: [{ id: "a-1", name: "Alice's Project Plan", updatedAt: new Date().toISOString() }],
            isLoading: false,
            isFetching: false,
        };
        const { container } = await renderList();

        expect(await screen.findByText("Alice's Project Plan")).toBeInTheDocument();
        expect(container.querySelector("[role='status']")).toBeNull();
    });

    test("during user switch (data: undefined, isFetching: true) prior user's titles are NOT visible", async () => {
        recentResult = { data: undefined, isLoading: false, isFetching: true };
        await renderList();

        expect(screen.queryByText("Alice's Project Plan")).not.toBeInTheDocument();
        expect(screen.queryByText(/no recent chats/i)).not.toBeInTheDocument();
    });

    test("when data resolves for user B, only user B's titles render", async () => {
        recentResult = {
            data: [{ id: "b-1", name: "Bob's Sprint Review", updatedAt: new Date().toISOString() }],
            isLoading: false,
            isFetching: false,
        };
        await renderList();

        expect(await screen.findByText("Bob's Sprint Review")).toBeInTheDocument();
        expect(screen.queryByText("Alice's Project Plan")).not.toBeInTheDocument();
    });
});
