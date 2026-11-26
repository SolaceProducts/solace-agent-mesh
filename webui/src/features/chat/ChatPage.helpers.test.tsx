// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { isValidElement } from "react";
import { fireEvent, render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import { deserializeChatMessages, stampMissingCreatedAt } from "@/providers/chat";

import type { MessageFE } from "@/types/chat";

import type { AgentCardInfo } from "@/types";

import {
    buildCollaborativeUsers,
    buildShareNotifications,
    chatAgentGate,
    chatBodyState,
    computeHeaderButtons,
    computeShareNotificationInsertions,
    forkOrShareButton,
    mobileSidePanelButton,
    presenceAvatarsForSession,
    type ShareNotification,
} from "./ChatPage.helpers";
import { ChatWelcomeScreen } from "./ChatWelcomeScreen";

const baseSharingParams = {
    sessionId: "s-1",
    sessionName: "Session",
    isCollaborativeSession: false,
    collaborativeUsers: [],
    sharedEditorUsers: [],
    currentUserEmail: "me@example.org",
    isForkingChat: false,
    isMobile: false,
    handleForkCollaborativeChat: vi.fn(() => Promise.resolve()),
    onShare: vi.fn(),
};

const renderWithRouter = (ui: React.ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);

describe("presenceAvatarsForSession", () => {
    test("returns null when no collaborative or shared users", () => {
        const result = presenceAvatarsForSession({ isCollaborativeSession: false, collaborativeUsers: [], sharedEditorUsers: [], currentUserEmail: undefined });
        expect(result).toBeNull();
    });

    test("returns avatars for collaborative session with users", () => {
        const users = [{ email: "a@x.com", name: "A", avatarColor: "blue" } as never];
        const result = presenceAvatarsForSession({ isCollaborativeSession: true, collaborativeUsers: users, sharedEditorUsers: [], currentUserEmail: "me@x.com" });
        expect(isValidElement(result)).toBe(true);
    });

    test("returns avatars for shared-editor users when not collaborative", () => {
        const users = [{ email: "b@x.com", name: "B", avatarColor: "red" } as never];
        const result = presenceAvatarsForSession({ isCollaborativeSession: false, collaborativeUsers: [], sharedEditorUsers: users, currentUserEmail: undefined });
        expect(isValidElement(result)).toBe(true);
    });
});

describe("forkOrShareButton", () => {
    test("collaborative session → Fork button", () => {
        const result = forkOrShareButton({ ...baseSharingParams, isCollaborativeSession: true });
        expect(isValidElement(result)).toBe(true);
        renderWithRouter(<>{result}</>);
    });

    test("non-collaborative → Share button", () => {
        const result = forkOrShareButton(baseSharingParams);
        expect(isValidElement(result)).toBe(true);
        renderWithRouter(<>{result}</>);
    });
});

describe("mobileSidePanelButton", () => {
    test("returns the activity-panel trigger Button", () => {
        const result = mobileSidePanelButton(vi.fn());
        expect(isValidElement(result)).toBe(true);
    });
});

describe("computeHeaderButtons", () => {
    const setIsMobileSidePanelOpen = vi.fn();
    test("no sharing, no mobile → undefined", () => {
        const out = computeHeaderButtons({ ...baseSharingParams, chatSharingEnabled: false, setIsMobileSidePanelOpen });
        expect(out).toBeUndefined();
    });

    test("mobile only → mobile-side-panel button", () => {
        const out = computeHeaderButtons({ ...baseSharingParams, isMobile: true, chatSharingEnabled: false, setIsMobileSidePanelOpen });
        expect(out).toHaveLength(1);
    });

    test("sharing + collaborative → fork button only", () => {
        const out = computeHeaderButtons({ ...baseSharingParams, isCollaborativeSession: true, chatSharingEnabled: true, setIsMobileSidePanelOpen });
        expect(out).toHaveLength(1);
    });

    test("sharing + non-collaborative → share button only", () => {
        const out = computeHeaderButtons({ ...baseSharingParams, chatSharingEnabled: true, setIsMobileSidePanelOpen });
        expect(out).toHaveLength(1);
    });

    test("sharing + collaborative users → avatars + fork", () => {
        const users = [{ email: "a@x.com", name: "A", avatarColor: "blue" } as never];
        const out = computeHeaderButtons({ ...baseSharingParams, isCollaborativeSession: true, collaborativeUsers: users, chatSharingEnabled: true, setIsMobileSidePanelOpen });
        expect(out).toHaveLength(2);
    });
});

describe("buildShareNotifications", () => {
    test("groups same-time same-level shares into one notification with humanized names", () => {
        const { notifications } = buildShareNotifications([
            { userEmail: "jane.doe@x.com", accessLevel: "resource_viewer", addedAt: "2024-01-01T00:00:00.000Z" },
            { userEmail: "john_smith@x.com", accessLevel: "resource_viewer", addedAt: "2024-01-01T00:00:00.000Z" },
        ]);
        expect(notifications).toHaveLength(1);
        expect(notifications[0]).toMatchObject({ variant: "shared-with-users", accessLevel: "viewer", names: ["Jane Doe", "John Smith"] });
    });

    test("different access levels at the same time produce separate groups", () => {
        const { notifications, editorUsers } = buildShareNotifications([
            { userEmail: "v@x.com", accessLevel: "resource_viewer", addedAt: "2024-01-01T00:00:00.000Z" },
            { userEmail: "e@x.com", accessLevel: "resource_editor", addedAt: "2024-01-01T00:00:00.000Z" },
        ]);
        expect(notifications).toHaveLength(2);
        expect(notifications.every(n => n.variant === "shared-with-users")).toBe(true);
        expect(editorUsers.map(u => u.email)).toEqual(["e@x.com"]);
    });

    test("a role-changed user yields both an initial-share and a role-changed notification", () => {
        const { notifications } = buildShareNotifications([{ userEmail: "a@x.com", accessLevel: "resource_editor", addedAt: "2024-01-02T00:00:00.000Z", originalAccessLevel: "resource_viewer", originalAddedAt: "2024-01-01T00:00:00.000Z" }]);
        expect(notifications.map(n => n.variant).sort()).toEqual(["role-changed", "shared-with-users"]);
        const change = notifications.find(n => n.variant === "role-changed");
        expect(change).toMatchObject({ fromAccessLevel: "viewer", toAccessLevel: "editor", names: ["A"] });
    });
});

describe("buildCollaborativeUsers", () => {
    test("includes the session owner even with no messages", () => {
        expect(buildCollaborativeUsers([], "owner@x.com", "Owner Name")).toEqual([{ id: "owner@x.com", name: "Owner Name", email: "owner@x.com", role: "collaborator", isOnline: true }]);
    });

    test("adds unique user-message senders and never duplicates the owner or non-user messages", () => {
        const messages = [
            { isUser: true, senderEmail: "owner@x.com", senderDisplayName: "Owner" },
            { isUser: true, senderEmail: "guest@x.com", senderDisplayName: "Guest" },
            { isUser: false, senderEmail: "agent@x.com" },
        ] as unknown as MessageFE[];
        expect(buildCollaborativeUsers(messages, "owner@x.com").map(u => u.email)).toEqual(["owner@x.com", "guest@x.com"]);
    });
});

describe("computeShareNotificationInsertions", () => {
    const notif = (timestamp: number): ShareNotification => ({ variant: "shared-with-users", names: ["A"], timestamp, accessLevel: "viewer" });

    test("inserts after the last message at-or-before the timestamp", () => {
        const messages = [{ createdAt: 100 }, { createdAt: 200 }, { createdAt: 300 }] as unknown as MessageFE[];
        expect(computeShareNotificationInsertions([notif(250)], messages)[0].insertAfterIndex).toBe(1);
    });

    test("falls back to the last message when none are at-or-before the timestamp", () => {
        const messages = [{ createdAt: 500 }, { createdAt: 600 }] as unknown as MessageFE[];
        expect(computeShareNotificationInsertions([notif(100)], messages)[0].insertAfterIndex).toBe(1);
    });

    test("anchors the divider at the share point for a live session (not the tail)", () => {
        const turn = (): MessageFE => ({ role: "agent", parts: [] }) as unknown as MessageFE;
        let messages = stampMissingCreatedAt([turn(), turn()], 100);
        const shareTimestamp = 250;
        messages = stampMissingCreatedAt([...messages, turn(), turn()], 400);

        expect(messages.map(m => m.createdAt)).toEqual([100, 100, 400, 400]);
        expect(computeShareNotificationInsertions([notif(shareTimestamp)], messages)[0].insertAfterIndex).toBe(1);
    });

    test("anchors the divider at the share point for a reloaded session with ISO-string createdAt", () => {
        const taskAt = (iso: string) => ({
            taskId: `t-${iso}`,
            createdAt: iso,
            messageBubbles: [
                { type: "user" as const, parts: [{ kind: "text" as const, text: "q" }] },
                { type: "agent" as const, parts: [{ kind: "text" as const, text: "a" }] },
            ],
        });
        const messages = [...deserializeChatMessages(taskAt("2026-06-08T15:26:23.000+00:00"), "s-1"), ...deserializeChatMessages(taskAt("2026-06-09T14:17:27.000+00:00"), "s-1")];
        const shareTimestamp = Date.parse("2026-06-08T15:28:10.907+00:00");

        expect(computeShareNotificationInsertions([notif(shareTimestamp)], messages)[0].insertAfterIndex).toBe(1);
    });
});

describe("chatAgentGate", () => {
    const agent = (overrides: Partial<AgentCardInfo>): AgentCardInfo => ({ name: "a1", displayName: "Agent 1", ...overrides }) as AgentCardInfo;
    const idleRefetch = () => ({ refetch: vi.fn(), isRefetching: false });

    test("shows the loading state while agents are still resolving", () => {
        const { getByText } = renderWithRouter(chatAgentGate({ isLoadingAgents: true, agents: [], agentsRefetch: idleRefetch() }));
        expect(getByText("Loading agents")).toBeTruthy();
    });

    test("shows the empty state when there are no chat-accessible agents", () => {
        const { getByText } = renderWithRouter(chatAgentGate({ isLoadingAgents: false, agents: [], agentsRefetch: idleRefetch() }));
        expect(getByText("No Agents Found")).toBeTruthy();
    });

    test("ignores workflow and internal agents when deciding emptiness", () => {
        const { getByText } = renderWithRouter(chatAgentGate({ isLoadingAgents: false, agents: [agent({ name: "wf", isWorkflow: true }), agent({ name: "int", isInternal: true })], agentsRefetch: idleRefetch() }));
        expect(getByText("No Agents Found")).toBeTruthy();
    });

    test("returns null when a chat-accessible agent exists", () => {
        expect(chatAgentGate({ isLoadingAgents: false, agents: [agent({})], agentsRefetch: idleRefetch() })).toBeNull();
    });

    test("renders a Refresh button that refetches agents", () => {
        const refetch = vi.fn();
        const { getByText } = renderWithRouter(chatAgentGate({ isLoadingAgents: false, agents: [], agentsRefetch: { refetch, isRefetching: false } }));
        fireEvent.click(getByText("Refresh"));
        expect(refetch).toHaveBeenCalledTimes(1);
    });

    test("disables the Refresh button while a refetch is in flight", () => {
        const { getByText } = renderWithRouter(chatAgentGate({ isLoadingAgents: false, agents: [], agentsRefetch: { refetch: vi.fn(), isRefetching: true } }));
        expect(getByText("Refresh").closest("button")?.disabled).toBe(true);
    });

    test("shows the error state (not the empty state) when discovery failed", () => {
        const refetch = vi.fn();
        const { getByText, queryByText } = renderWithRouter(chatAgentGate({ isLoadingAgents: false, hasAgentsError: true, agents: [], agentsRefetch: { refetch, isRefetching: false } }));
        expect(getByText("Unable to Load Agents")).toBeTruthy();
        expect(queryByText("No Agents Found")).toBeNull();
        fireEvent.click(getByText("Refresh"));
        expect(refetch).toHaveBeenCalledTimes(1);
    });

    test("falls through (lets the user chat) on a partial failure where usable agents still loaded", () => {
        expect(chatAgentGate({ isLoadingAgents: false, hasAgentsError: true, agents: [agent({})], agentsRefetch: idleRefetch() })).toBeNull();
    });
});

describe("chatBodyState — welcome fallthrough", () => {
    const baseParams = {
        isLoadingSession: false,
        noAgentSpecified: false,
        isAwaitingPinnedAgent: false,
        pinnedAgentTimedOut: false,
        isWelcomeState: true,
        isResponding: false,
        isLoadingAgents: false,
        agents: [{ name: "a1", displayName: "Agent 1" }] as AgentCardInfo[],
        selectedAgentName: undefined,
        agentsRefetch: { refetch: vi.fn(), isRefetching: false },
    };

    test("renders the welcome screen when a chat-accessible agent exists", () => {
        const result = chatBodyState(baseParams);
        expect(isValidElement(result)).toBe(true);
        expect((result as React.ReactElement).type).toBe(ChatWelcomeScreen);
    });

    test("delegates to chatAgentGate for the empty state", () => {
        const { getByText } = renderWithRouter(chatBodyState({ ...baseParams, agents: [] }));
        expect(getByText("No Agents Found")).toBeTruthy();
    });

    test("forwards the refetch handler through to the empty-state Refresh button", () => {
        const refetch = vi.fn();
        const { getByText } = renderWithRouter(chatBodyState({ ...baseParams, agents: [], agentsRefetch: { refetch, isRefetching: false } }));
        fireEvent.click(getByText("Refresh"));
        expect(refetch).toHaveBeenCalledTimes(1);
    });

    test("forwards the error state through to the gate when discovery failed", () => {
        const refetch = vi.fn();
        const { getByText } = renderWithRouter(chatBodyState({ ...baseParams, agents: [], hasAgentsError: true, agentsRefetch: { refetch, isRefetching: false } }));
        expect(getByText("Unable to Load Agents")).toBeTruthy();
        fireEvent.click(getByText("Refresh"));
        expect(refetch).toHaveBeenCalledTimes(1);
    });
});
