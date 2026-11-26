// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { focusManager } from "@tanstack/react-query";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { MockEventSource } from "@/testing/helpers/mockEventSource";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { NotificationsBell } from "./NotificationsBell";
import { formatNotificationTime } from "./notificationTime";
import { INBOX_REFETCH_DEBOUNCE_MS } from "./useLiveNotificationInbox";

const mockNavigate = vi.fn();
vi.mock("react-router-dom", async importOriginal => {
    const actual = await importOriginal<typeof import("react-router-dom")>();
    return { ...actual, useNavigate: () => mockNavigate };
});

const { mockGet, mockPost } = vi.hoisted(() => ({ mockGet: vi.fn(), mockPost: vi.fn() }));
vi.mock("@/api/client", () => ({ api: { webui: { get: mockGet, post: mockPost } } }));

const mockMarkShareViewed = vi.fn();
vi.mock("@/api/share", async orig => ({
    ...(await orig<typeof import("@/api/share")>()),
    useMarkShareViewed: () => ({ mutate: mockMarkShareViewed }),
}));

const recentResponse = {
    data: [
        { id: "h1", title: "Approval needed", body: "", status: "unread", createdAt: "2026-06-09T10:00:00.000Z", requestId: "req-1", surface: { surfaceId: "s", components: [] } },
        { id: "n1", title: "Report ready", body: "", status: "read", createdAt: "2026-06-09T09:00:00.000Z" },
    ],
    meta: { pagination: { pageNumber: 1, pageSize: 12, totalPages: 1, count: 2, nextPage: null } },
};
const unreadCountResponse = {
    data: [],
    meta: { pagination: { pageNumber: 1, pageSize: 1, totalPages: 7, count: 7, nextPage: null } },
};

const requestsMatching = (matches: (url: string) => boolean) => mockGet.mock.calls.filter(([url]) => matches(String(url))).length;
const isInboxRequest = (url: string) => url.includes("/api/v1/notify/inbox?");
const unreadRequests = () => requestsMatching(url => isInboxRequest(url) && url.includes("status=unread"));
const recentListRequests = () => requestsMatching(url => isInboxRequest(url) && !url.includes("status=unread"));

beforeEach(() => {
    mockNavigate.mockReset();
    mockGet.mockReset();
    mockPost.mockReset();
    mockMarkShareViewed.mockReset();
    mockGet.mockImplementation((url: string) => Promise.resolve(url.includes("status=unread") ? unreadCountResponse : recentResponse));
    mockPost.mockResolvedValue(undefined);
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
});

afterEach(() => {
    vi.clearAllMocks();
    vi.unstubAllGlobals();
});

const NOTIFICATION_STREAM = "/api/v1/sse/notifications";

async function notificationStream(index = 0): Promise<MockEventSource> {
    await waitFor(() => expect(MockEventSource.created(NOTIFICATION_STREAM).length).toBeGreaterThan(index));
    return MockEventSource.created(NOTIFICATION_STREAM)[index];
}

const withUnreadCount = (count: number) => (url: string) => Promise.resolve(url.includes("status=unread") ? { data: [], meta: { pagination: { ...unreadCountResponse.meta.pagination, count } } } : recentResponse);

describe("NotificationsBell", () => {
    it("shows the unread badge, opens the flyout, and lists recent notifications", async () => {
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });

        const bell = await screen.findByRole("button", { name: /notifications \(7 unread\)/i });
        expect(bell).toHaveTextContent("7");

        fireEvent.click(bell);

        expect(await screen.findByText("Approval needed")).toBeInTheDocument();
        expect(screen.getByText("Report ready")).toBeInTheDocument();
        expect(screen.getByText("Needs response soon")).toBeInTheDocument();

        fireEvent.click(screen.getByText("Report ready"));
        expect(mockNavigate).toHaveBeenCalledWith("/notifications?focus=n1");
    });

    it("shows each item's time day first with a lowercase am or pm, as the page does", async () => {
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: /notifications \(7 unread\)/i }));

        expect(await screen.findByText(formatNotificationTime("2026-06-09T09:00:00.000Z"))).toBeInTheDocument();
    });

    it("refreshes the badge and the recent list when a notification arrives, and announces it", async () => {
        mockGet.mockImplementation(withUnreadCount(7));
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByRole("button", { name: /notifications \(7 unread\)/i })).toBeInTheDocument();
        const stream = await notificationStream();
        act(() => stream.open());
        const recentBefore = recentListRequests();

        mockGet.mockImplementation(withUnreadCount(8));
        act(() => stream.emit("notification_created", JSON.stringify({ id: "n9" })));

        expect(await screen.findByRole("button", { name: /notifications \(8 unread\)/i })).toBeInTheDocument();
        expect(screen.getByRole("status")).toHaveTextContent("8 unread notifications");
        await waitFor(() => expect(recentListRequests()).toBeGreaterThan(recentBefore));
    });

    it("refetches the inbox once for a burst of notifications", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });
            await screen.findByRole("button", { name: /notifications \(7 unread\)/i });
            const stream = await notificationStream();
            act(() => stream.open());
            await vi.advanceTimersByTimeAsync(100);
            const before = unreadRequests();

            act(() => {
                for (let i = 0; i < 5; i++) {
                    stream.emit("notification_created", JSON.stringify({ id: `n${i}` }));
                }
            });
            await vi.advanceTimersByTimeAsync(INBOX_REFETCH_DEBOUNCE_MS + 100);

            expect(unreadRequests()).toBe(before + 1);
        } finally {
            vi.useRealTimers();
        }
    });

    it("does not poll the inbox", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });
            const bell = await screen.findByRole("button", { name: /notifications \(7 unread\)/i });
            fireEvent.click(bell);
            await vi.advanceTimersByTimeAsync(100);
            const before = mockGet.mock.calls.length;

            await vi.advanceTimersByTimeAsync(5 * 60_000);

            expect(mockGet.mock.calls.length).toBe(before);
        } finally {
            vi.useRealTimers();
        }
    });

    it("refreshes the inbox when the notification stream reconnects, not when it first connects", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });
            await screen.findByRole("button", { name: /notifications \(7 unread\)/i });
            const first = await notificationStream();
            const beforeOpen = unreadRequests();
            act(() => first.open());
            await vi.advanceTimersByTimeAsync(100);
            expect(unreadRequests()).toBe(beforeOpen);

            mockGet.mockImplementation(withUnreadCount(9));
            act(() => first.fail());
            await vi.advanceTimersByTimeAsync(2_000);
            const second = await notificationStream(1);
            act(() => second.open());

            expect(await screen.findByRole("button", { name: /notifications \(9 unread\)/i })).toBeInTheDocument();
        } finally {
            vi.useRealTimers();
        }
    });

    it("refreshes the inbox when the notification stream connects after failing to connect", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });
            await screen.findByRole("button", { name: /notifications \(7 unread\)/i });
            const first = await notificationStream();

            mockGet.mockImplementation(withUnreadCount(9));
            act(() => first.fail());
            await vi.advanceTimersByTimeAsync(2_000);
            const second = await notificationStream(1);
            act(() => second.open());

            expect(await screen.findByRole("button", { name: /notifications \(9 unread\)/i })).toBeInTheDocument();
        } finally {
            vi.useRealTimers();
        }
    });

    it("refreshes the badge and the recent list when the flyout opens", async () => {
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });
        const bell = await screen.findByRole("button", { name: /notifications \(7 unread\)/i });
        await waitFor(() => expect(recentListRequests()).toBeGreaterThan(0));
        const recentBefore = recentListRequests();

        mockGet.mockImplementation(withUnreadCount(8));
        fireEvent.click(bell);

        expect(await screen.findByRole("button", { name: /notifications \(8 unread\)/i })).toBeInTheDocument();
        await waitFor(() => expect(recentListRequests()).toBeGreaterThan(recentBefore));
    });

    it("refreshes the unread count as soon as the tab regains focus", async () => {
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });
        await screen.findByRole("button", { name: /notifications \(7 unread\)/i });
        await waitFor(() => expect(unreadRequests()).toBe(1));

        act(() => {
            focusManager.setFocused(false);
            focusManager.setFocused(true);
        });

        await waitFor(() => expect(unreadRequests()).toBe(2));
    });

    it("hides the visual badge from assistive tech so the count is not read twice", async () => {
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });
        await screen.findByRole("button", { name: /notifications \(7 unread\)/i });
        expect(screen.getByText("7")).toHaveAttribute("aria-hidden", "true");
    });

    it("announces a single unread notification in the singular", async () => {
        mockGet.mockImplementation((url: string) => Promise.resolve(url.includes("status=unread") ? { data: [], meta: { pagination: { ...unreadCountResponse.meta.pagination, count: 1 } } } : recentResponse));
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await screen.findByRole("button", { name: /notifications \(1 unread\)/i });
        expect(screen.getByRole("status")).toHaveTextContent(/^1 unread notification$/);
    });

    it("does not flag a resolved HIL prompt as 'Action required' in the flyout", async () => {
        mockGet.mockImplementation((url: string) =>
            url.includes("status=unread")
                ? Promise.resolve(unreadCountResponse)
                : Promise.resolve({
                      data: [{ id: "hr", title: "Expired approval", body: "", status: "unread", createdAt: "2026-06-09T10:00:00.000Z", requestId: "req-r", surface: { surfaceId: "s", components: [] }, promptState: "resolved" }],
                      meta: { pagination: { pageNumber: 1, pageSize: 12, totalPages: 1, count: 1, nextPage: null } },
                  })
        );
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });
        fireEvent.click(await screen.findByRole("button", { name: /notifications/i }));

        expect(await screen.findByText("Expired approval")).toBeInTheDocument();
        expect(screen.queryByText(/Needs response/)).not.toBeInTheDocument();
    });

    it("marks all read, opens the inbox, and opens settings from the footer/header links", async () => {
        const onOpenSettings = vi.fn();
        renderWithProviders(<NotificationsBell onOpenSettings={onOpenSettings} />, { featureFlags: { notify_service: true }, configContextValues: {} });
        fireEvent.click(await screen.findByRole("button", { name: /notifications/i }));

        fireEvent.click(await screen.findByRole("button", { name: /mark all as read/i }));
        await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/read-all"));

        fireEvent.click(screen.getByRole("button", { name: /see all/i }));
        expect(mockNavigate).toHaveBeenCalledWith("/notifications");

        fireEvent.click(await screen.findByRole("button", { name: /notifications/i }));
        fireEvent.click(screen.getByRole("button", { name: /^settings$/i }));
        expect(onOpenSettings).toHaveBeenCalledTimes(1);
    });

    it("disables mark-all and shows the caught-up state when there is nothing unread", async () => {
        mockGet.mockImplementation((url: string) =>
            Promise.resolve(
                url.includes("status=unread")
                    ? { data: [], meta: { pagination: { pageNumber: 1, pageSize: 1, totalPages: 0, count: 0, nextPage: null } } }
                    : { data: [], meta: { pagination: { pageNumber: 1, pageSize: 12, totalPages: 0, count: 0, nextPage: null } } }
            )
        );
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });

        const bell = await screen.findByRole("button", { name: /^notifications$/i });
        expect(bell).not.toHaveTextContent(/[0-9]/);
        expect(screen.getByRole("status")).toBeEmptyDOMElement();
        fireEvent.click(bell);

        expect(await screen.findByText(/You're all caught up/i)).toBeInTheDocument();
        const markAll = screen.getByRole("button", { name: /mark all as read/i });
        expect(markAll).toBeDisabled();
    });

    it("shows no CTA for an item whose action opens nothing", async () => {
        mockGet.mockImplementation((url: string) =>
            Promise.resolve(
                url.includes("status=unread")
                    ? unreadCountResponse
                    : {
                          data: [
                              { id: "a1", title: "Plain note", body: "", status: "read", createdAt: "2026-06-09T09:00:00.000Z" },
                              { id: "a2", title: "Label only", body: "", status: "read", createdAt: "2026-06-09T08:00:00.000Z", action: { label: "Go to session" } },
                          ],
                          meta: { pagination: { pageNumber: 1, pageSize: 12, totalPages: 1, count: 2, nextPage: null } },
                      }
            )
        );
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: /notifications/i }));

        expect(await screen.findByText("Label only")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "View Chat" })).not.toBeInTheDocument();
    });

    it("flags an input request that was read but still awaits an answer", async () => {
        mockGet.mockImplementation((url: string) =>
            Promise.resolve(
                url.includes("status=unread")
                    ? unreadCountResponse
                    : {
                          data: [{ id: "hp", title: "Approval pending", body: "", status: "read", createdAt: "2026-06-09T10:00:00.000Z", requestId: "req-p", surface: { surfaceId: "s", components: [] }, promptState: "pending" }],
                          meta: { pagination: { pageNumber: 1, pageSize: 12, totalPages: 1, count: 1, nextPage: null } },
                      }
            )
        );
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: /notifications/i }));

        expect(await screen.findByText("Approval pending")).toBeInTheDocument();
        expect(screen.getByText("Needs response soon")).toBeInTheDocument();
    });

    it("renders a deep-link CTA in a flyout row and opens the source session on click", async () => {
        const mockSwitch = vi.fn().mockResolvedValue(undefined);
        mockGet.mockImplementation((url: string) =>
            Promise.resolve(
                url.includes("status=unread")
                    ? unreadCountResponse
                    : {
                          data: [{ id: "s1", title: "Scheduled task done", body: "", status: "read", createdAt: "2026-06-09T09:00:00.000Z", action: { label: "View result", sessionId: "sess-7", agentId: "Reporter" } }],
                          meta: { pagination: { pageNumber: 1, pageSize: 12, totalPages: 1, count: 1, nextPage: null } },
                      }
            )
        );
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {}, chatContextValues: { handleSwitchSession: mockSwitch } });

        fireEvent.click(await screen.findByRole("button", { name: /notifications/i }));
        fireEvent.click(await screen.findByRole("button", { name: "View Chat" }));

        await waitFor(() => expect(mockSwitch).toHaveBeenCalledWith("sess-7"));
        expect(mockNavigate).toHaveBeenCalledWith("/chat");
        await act(() => new Promise(resolve => setTimeout(resolve, 0)));
        expect(mockPost).not.toHaveBeenCalledWith("/api/v1/notify/inbox/s1/read");
    });

    it("labels an in-app path action with the producer's wording and navigates to it", async () => {
        mockGet.mockImplementation((url: string) =>
            Promise.resolve(
                url.includes("status=unread")
                    ? unreadCountResponse
                    : {
                          data: [{ id: "b1", title: "Agent Mesh budget reached", body: "", status: "unread", createdAt: "2026-06-09T09:00:00.000Z", action: { label: "Review budget", path: "/budgets" } }],
                          meta: { pagination: { pageNumber: 1, pageSize: 12, totalPages: 1, count: 1, nextPage: null } },
                      }
            )
        );
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: /notifications/i }));
        fireEvent.click(await screen.findByRole("button", { name: "Review budget" }));

        expect(screen.queryByRole("button", { name: "View Chat" })).not.toBeInTheDocument();
        expect(mockNavigate).toHaveBeenCalledWith("/budgets");
    });

    it("opens the shared-chat view from a share notification's CTA", async () => {
        const mockSwitch = vi.fn().mockResolvedValue(undefined);
        mockGet.mockImplementation((url: string) =>
            Promise.resolve(
                url.includes("status=unread")
                    ? unreadCountResponse
                    : {
                          data: [
                              {
                                  id: "sh1",
                                  title: "owner@example.com shared a chat with you",
                                  body: '"Stonehenge Overview and History"\nhttps://host/#/share/share-9',
                                  status: "unread",
                                  source: "chat_share",
                                  createdAt: "2026-06-09T09:00:00.000Z",
                                  action: { label: "View shared chat", shareId: "share-9" },
                              },
                          ],
                          meta: { pagination: { pageNumber: 1, pageSize: 12, totalPages: 1, count: 1, nextPage: null } },
                      }
            )
        );
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {}, chatContextValues: { handleSwitchSession: mockSwitch } });

        fireEvent.click(await screen.findByRole("button", { name: /notifications/i }));
        fireEvent.click(await screen.findByRole("button", { name: "View Chat" }));

        expect(mockNavigate).toHaveBeenCalledWith("/shared-chat/share-9");
        expect(mockSwitch).not.toHaveBeenCalled();
        expect(mockMarkShareViewed).toHaveBeenCalledWith("share-9");
        await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/sh1/read"));
    });

    it("renders icon-only with a tooltip title when collapsed", async () => {
        renderWithProviders(<NotificationsBell collapsed />, { featureFlags: { notify_service: true }, configContextValues: {} });

        const bell = await screen.findByRole("button", { name: /notifications \(7 unread\)/i });
        expect(bell).toHaveAttribute("title", "Notifications");
        expect(bell).toHaveTextContent("7");
        expect(bell.querySelector("span")).not.toHaveTextContent(/^Notifications$/);
    });

    it("captions a categorized item with its category label and leaves uncategorized items bare", async () => {
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/preferences/categories")) {
                return Promise.resolve({
                    data: [{ category: "scheduled_task", label: "Scheduled task results", enabled: true }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            }
            if (url.includes("status=unread")) return Promise.resolve(unreadCountResponse);
            return Promise.resolve({
                data: [
                    { id: "s1", title: "Nightly report", body: "", status: "unread", category: "scheduled_task", createdAt: "2026-06-09T10:00:00.000Z" },
                    { id: "s2", title: "Unknown kind", body: "", status: "read", category: "retired_category", createdAt: "2026-06-09T09:00:00.000Z" },
                ],
                meta: recentResponse.meta,
            });
        });
        renderWithProviders(<NotificationsBell />, { featureFlags: { notify_service: true }, configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: /notifications \(7 unread\)/i }));

        const caption = await screen.findByText("Scheduled task results");
        expect(caption.closest("button")).toHaveTextContent(/Scheduled task results\s*Nightly report/);
        expect(screen.getByText("Unknown kind").closest("button")).not.toHaveTextContent(/retired_category/i);
    });
});
