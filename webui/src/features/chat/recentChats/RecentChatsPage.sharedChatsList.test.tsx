// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { SharedWithMeItem } from "@/types/share";

import { SharedChatsList } from "./RecentChatsPage";

const item = (over: Partial<SharedWithMeItem>): SharedWithMeItem => ({
    id: "share-1",
    title: "Shared Chat",
    ownerEmail: "owner@example.com",
    accessLevel: "resource_viewer",
    sharedAt: new Date(0).toISOString(),
    shareUrl: "https://example.com/share-1",
    sessionId: null,
    ...over,
});

const renderList = (props: Partial<React.ComponentProps<typeof SharedChatsList>> = {}) => {
    const defaults = {
        sharedChats: [] as SharedWithMeItem[],
        isLoading: false,
        chatRoute: "/chat",
        onSwitchSession: vi.fn(),
        onMarkViewed: vi.fn(),
        navigate: vi.fn(),
    };
    const merged = { ...defaults, ...props };
    render(<SharedChatsList {...merged} />);
    return merged;
};

describe("SharedChatsList", () => {
    test("renders neither the empty state nor any item while loading", () => {
        renderList({ isLoading: true });
        expect(screen.queryByText("No shared chats")).not.toBeInTheDocument();
    });

    test("renders the empty state when there are no shared chats", () => {
        renderList();
        expect(screen.getByText("No shared chats")).toBeInTheDocument();
    });

    test("viewer item navigates to the shared-chat view (no session switch)", async () => {
        const user = userEvent.setup();
        const { navigate, onSwitchSession } = renderList({ sharedChats: [item({ id: "abc", accessLevel: "resource_viewer" })] });

        await user.click(screen.getByText("Shared Chat"));

        expect(navigate).toHaveBeenCalledWith("/shared-chat/abc");
        expect(onSwitchSession).not.toHaveBeenCalled();
    });

    test("editor item switches the session, then navigates to the chat route", async () => {
        const user = userEvent.setup();
        const { navigate, onSwitchSession } = renderList({
            sharedChats: [item({ accessLevel: "resource_editor", sessionId: "sess-9" })],
            chatRoute: "/agent-mode/chat",
            onSwitchSession: vi.fn().mockResolvedValue(undefined),
        });

        await user.click(screen.getByText("Shared Chat"));

        expect(onSwitchSession).toHaveBeenCalledWith("sess-9");
        expect(navigate).toHaveBeenCalledWith("/agent-mode/chat");
    });

    test("shows the unseen indicator for a share the recipient has never opened", () => {
        renderList({ sharedChats: [item({ viewedAt: null })] });
        expect(screen.getByLabelText("Unseen updates")).toBeInTheDocument();
    });

    test("shows the unseen indicator when the owner changed the share after it was read", () => {
        renderList({
            sharedChats: [item({ sharedAt: new Date(2_000).toISOString(), viewedAt: new Date(1_000).toISOString() })],
        });
        expect(screen.getByLabelText("Unseen updates")).toBeInTheDocument();
    });

    test("hides the unseen indicator once the share has been opened", () => {
        renderList({
            sharedChats: [item({ sharedAt: new Date(1_000).toISOString(), viewedAt: new Date(2_000).toISOString() })],
        });
        expect(screen.queryByLabelText("Unseen updates")).not.toBeInTheDocument();
    });

    test("marks the share viewed when it is opened", async () => {
        const user = userEvent.setup();
        const { onMarkViewed } = renderList({ sharedChats: [item({ id: "abc" })] });

        await user.click(screen.getByText("Shared Chat"));

        expect(onMarkViewed).toHaveBeenCalledWith("abc");
    });

    test("marks the share viewed on the editor path too, where the destination cannot", async () => {
        const user = userEvent.setup();
        const { onMarkViewed } = renderList({
            sharedChats: [item({ id: "edit-1", accessLevel: "resource_editor", sessionId: "sess-9" })],
            onSwitchSession: vi.fn().mockResolvedValue(undefined),
        });

        await user.click(screen.getByText("Shared Chat"));

        expect(onMarkViewed).toHaveBeenCalledWith("edit-1");
    });
});
