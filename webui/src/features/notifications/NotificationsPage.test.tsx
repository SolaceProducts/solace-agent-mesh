// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { focusManager } from "@tanstack/react-query";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, Outlet, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";

import { notificationKeys } from "@/api/notifications";

import { queryClient } from "@/providers/query/QueryClient";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { NotificationsPage } from "./NotificationsPage";
import { formatNotificationTime } from "./notificationTime";

async function expandFirstRow() {
    fireEvent.click((await screen.findAllByRole("button", { name: "Expand details" }))[0]);
}

const mockNavigate = vi.fn();
vi.mock("react-router-dom", async orig => ({
    ...(await orig<typeof import("react-router-dom")>()),
    useNavigate: () => mockNavigate,
}));

const mockMarkShareViewed = vi.fn();
vi.mock("@/api/share", async orig => ({
    ...(await orig<typeof import("@/api/share")>()),
    useMarkShareViewed: () => ({ mutate: mockMarkShareViewed }),
}));

vi.mock("@/api/sessions/useSessionName", () => ({
    useSessionName: (sessionId: string | undefined) => (sessionId === "chat-7" ? "Billing rollout" : undefined),
}));

const mockGet = vi.fn();
const mockPost = vi.fn();
const mockPut = vi.fn();
const mockDelete = vi.fn();
vi.mock("@/api/client", () => ({
    api: {
        webui: {
            get: (...args: unknown[]) => mockGet(...args),
            post: (...args: unknown[]) => mockPost(...args),
            put: (...args: unknown[]) => mockPut(...args),
            delete: (...args: unknown[]) => mockDelete(...args),
        },
    },
}));

const inboxResponse = {
    data: [
        { id: "n1", title: "Report ready", body: "Your report is ready.", status: "unread", createdAt: "2026-06-01T10:00:00.000Z" },
        { id: "n2", title: "Earlier note", body: "Already seen", status: "read", createdAt: "2026-06-01T09:00:00.000Z" },
    ],
    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 2, nextPage: null } },
};

const shareURL = "https://sam.example.test/#/share/share-9";
const shareInboxResponse = {
    data: [
        {
            id: "sh1",
            title: "owner@example.com shared a chat with you",
            body: `"Stonehenge Overview and History"\n${shareURL}`,
            status: "unread",
            source: "chat_share",
            createdAt: "2026-06-01T10:00:00.000Z",
            action: { label: "View shared chat", shareId: "share-9" },
        },
    ],
    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
};

const prefsResponse = {
    data: [
        { channelType: "webhook", destinationKey: "wh_abc123", enabled: false, destination: "hooks.example.com" },
        { channelType: "connector", destinationKey: "slack_send_message_x", enabled: true, destination: "Slack" },
    ],
    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 2, nextPage: null } },
};

beforeEach(() => {
    mockNavigate.mockReset();
    mockMarkShareViewed.mockReset();
    mockGet.mockReset();
    mockPost.mockReset();
    mockPut.mockReset();
    mockGet.mockImplementation((url: string) => {
        if (url.includes("/notify/inbox")) return Promise.resolve(inboxResponse);
        if (url.includes("/notify/preferences")) return Promise.resolve(prefsResponse);
        return Promise.resolve({});
    });
    mockPost.mockResolvedValue(undefined);
    mockPut.mockResolvedValue({ channelType: "webhook", enabled: true });
    mockDelete.mockResolvedValue(undefined);
});

afterEach(() => vi.clearAllMocks());

describe("NotificationsPage", () => {
    it("renders the inbox and marks a notification read from the row menu", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByText("Report ready")).toBeInTheDocument();
        expect(screen.getByText("Earlier note")).toBeInTheDocument();

        const user = userEvent.setup();
        await user.click(screen.getAllByRole("button", { name: /notification actions/i })[0]);
        await user.click(await screen.findByRole("menuitem", { name: /mark as read/i }));
        await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/n1/read"));
    });

    it("does not refetch a fresh inbox when the window regains focus", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText("Report ready")).toBeInTheDocument();
        const inboxRequests = () => mockGet.mock.calls.filter(([url]) => String(url).includes("/notify/inbox")).length;
        const before = inboxRequests();

        act(() => {
            focusManager.setFocused(false);
            focusManager.setFocused(true);
        });

        await new Promise(resolve => setTimeout(resolve, 50));
        expect(inboxRequests()).toBe(before);
    });

    it("redirects to /chat when the notify_service flag is off", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: false }, configContextValues: {} });
        await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith("/chat", { replace: true }));
    });

    it("sends the user to the chat to answer an input request that belongs to one, showing what it asks without its buttons", async () => {
        const hilSurface = {
            surfaceId: "req-c1",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                { id: "title", component: "Text", text: { literalString: "Approval Required" }, variant: "h3" },
                { id: "info", component: "Text", text: { literalString: 'Agent "OpsAgent" wants to run tool: restart_service' }, variant: "body" },
                { id: "approve-lbl", component: "Text", text: { literalString: "Approve" } },
                { id: "approve", component: "Button", variant: "primary", child: "approve-lbl", action: { event: { name: "submit", context: { approved: true }, completionText: "Approved" } } },
                { id: "actions", component: "Row", children: { explicitList: ["approve"] } },
                { id: "countdown", component: "Text", text: { literalString: "Expires in 30 minutes" }, variant: "caption" },
                { id: "main-col", component: "Column", children: { explicitList: ["title", "info", "actions", "countdown"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [
                        { id: "c1", title: "Your input is requested", body: "", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-c1", promptState: "pending", surface: hilSurface, action: { label: "", sessionId: "chat-7" } },
                    ],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByRole("button", { name: "Respond in Chat" })).toBeInTheDocument();
        await expandFirstRow();
        expect(await screen.findByText('Agent "OpsAgent" wants to run tool: restart_service')).toBeInTheDocument();
        expect(await screen.findByText("Billing rollout")).toBeInTheDocument();
        expect(screen.getByText(/Respond to the request in/)).toBeInTheDocument();
        expect(screen.getByText("Expires in 30 minutes")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
    });

    it("opens a waiting choice question's details with Selection Required and no options", async () => {
        const question = {
            surfaceId: "req-q1",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                { id: "q0-label", component: "Text", text: { literalString: "Which region should the summary cover?" }, variant: "h3" },
                { id: "q0-picker", component: "ChoicePicker", variant: "mutuallyExclusive", options: [{ label: { literalString: "EMEA" }, value: "emea" }], value: { path: "/answers/q0" } },
                { id: "q0-other", component: "TextField", label: { literalString: "Other" } },
                { id: "main-col", component: "Column", children: { explicitList: ["q0-label", "q0-picker", "q0-other"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "q1", title: "Your input is requested", body: "", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-q1", promptState: "pending", surface: question, action: { label: "", sessionId: "chat-7" } }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();
        expect(await screen.findByText("Selection Required")).toBeInTheDocument();
        expect(screen.getByText("Which region should the summary cover?").closest("div.flex-col")).not.toHaveClass("px-5");
        expect(screen.queryByRole("radio")).not.toBeInTheDocument();
        expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    });

    it("renders a HIL prompt as an interactive card and submits the answer to the respond endpoint", async () => {
        const hilSurface = {
            surfaceId: "req-h1",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                { id: "q", component: "Text", text: { literalString: "Approve the deploy?" }, variant: "body" },
                { id: "approve-lbl", component: "Text", text: { literalString: "Approve" } },
                { id: "approve", component: "Button", variant: "primary", child: "approve-lbl", action: { event: { name: "submit", context: { answers: { q0: "yes" } }, completionText: "Submitted" } } },
                { id: "cancel-lbl", component: "Text", text: { literalString: "Decline" } },
                { id: "cancel", component: "Button", child: "cancel-lbl", action: { event: { name: "cancel", completionText: "Declined" } } },
                { id: "main-col", component: "Column", children: { explicitList: ["q", "approve", "cancel"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "h1", title: "Approval needed", body: "", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-h1", surface: hilSurface }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByText("Needs response soon")).toBeInTheDocument();
        expect(screen.queryByText("Approve the deploy?")).not.toBeInTheDocument();
        await expandFirstRow();
        expect(await screen.findByText("Approve the deploy?")).toBeInTheDocument();
        expect(mockPost).not.toHaveBeenCalledWith("/api/v1/notify/inbox/h1/read");

        fireEvent.click(screen.getByRole("button", { name: /approve/i }));
        await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/h1/respond", { status: "answered", data: { answers: { q0: "yes" } } }));
        expect(await screen.findByText(/Submitted/)).toBeInTheDocument();
    });

    it("submits a cancellation with empty data when the user declines a HIL prompt", async () => {
        const hilSurface = {
            surfaceId: "req-h2",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                { id: "lbl", component: "Text", text: { literalString: "Decline" } },
                { id: "cancel", component: "Button", child: "lbl", action: { event: { name: "cancel", completionText: "Declined" } } },
                { id: "main-col", component: "Column", children: { explicitList: ["cancel"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "h2", title: "Approval", body: "", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-h2", surface: hilSurface }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();
        fireEvent.click(await screen.findByRole("button", { name: /decline/i }));
        await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/h2/respond", { status: "cancelled", data: {} }));
    });

    it("surfaces an error when the HIL respond call fails", async () => {
        const hilSurface = {
            surfaceId: "req-h3",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                { id: "lbl", component: "Text", text: { literalString: "Approve" } },
                { id: "approve", component: "Button", variant: "primary", child: "lbl", action: { event: { name: "submit", context: {}, completionText: "ok" } } },
                { id: "main-col", component: "Column", children: { explicitList: ["approve"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "h3", title: "Approval", body: "", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-h3", surface: hilSurface }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        mockPost.mockImplementation((url: string) => {
            if (!url.includes("/respond")) return Promise.resolve(undefined);
            const err = new Error("this request is no longer awaiting input (already answered, cancelled, or expired)") as Error & { status?: number };
            err.status = 409;
            return Promise.reject(err);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();
        const inboxGetsBefore = mockGet.mock.calls.filter(c => String(c[0]).includes("/notify/inbox")).length;
        fireEvent.click(await screen.findByRole("button", { name: /approve/i }));
        expect(await screen.findByText(/no longer awaiting input/i)).toBeInTheDocument();
        expect(screen.queryByText(/please try again/i)).not.toBeInTheDocument();
        await waitFor(() => expect(mockGet.mock.calls.filter(c => String(c[0]).includes("/notify/inbox")).length).toBeGreaterThan(inboxGetsBefore));
    });

    it("keeps the generic retry copy when the HIL respond fails without an HTTP status (network error)", async () => {
        const hilSurface = {
            surfaceId: "req-net",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                { id: "lbl", component: "Text", text: { literalString: "Approve" } },
                { id: "approve", component: "Button", variant: "primary", child: "lbl", action: { event: { name: "submit", context: {}, completionText: "ok" } } },
                { id: "main-col", component: "Column", children: { explicitList: ["approve"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "hn", title: "Approval", body: "", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-net", surface: hilSurface }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        mockPost.mockImplementation((url: string) => (url.includes("/respond") ? Promise.reject(new Error("Failed to fetch")) : Promise.resolve(undefined)));
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();
        fireEvent.click(await screen.findByRole("button", { name: /approve/i }));
        expect(await screen.findByText(/please try again/i)).toBeInTheDocument();
        expect(screen.queryByText(/failed to fetch/i)).not.toBeInTheDocument();
    });

    it("says who answered an approval answered elsewhere instead of calling it expired", async () => {
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [
                        {
                            id: "ha",
                            title: "Approval",
                            body: "",
                            status: "unread",
                            createdAt: "2026-06-01T10:00:00.000Z",
                            requestId: "req-ha",
                            surface: { surfaceId: "req-ha", components: [] },
                            promptState: "resolved",
                            promptOutcome: "approved",
                            promptAnsweredBy: "Dana Kim",
                        },
                    ],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByText("Approved by Dana Kim")).toBeInTheDocument();
        expect(screen.queryByText("Expired")).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: /expand details/i }));
        expect(await screen.findByText("This request is no longer awaiting input: Approved by Dana Kim.")).toBeInTheDocument();
    });

    it("renders a resolved HIL prompt inert (no answerable card) with an expired note", async () => {
        const hilSurface = {
            surfaceId: "req-hr",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                { id: "q", component: "Text", text: { literalString: "Approve the deploy?" }, variant: "body" },
                { id: "lbl", component: "Text", text: { literalString: "Approve" } },
                { id: "approve", component: "Button", variant: "primary", child: "lbl", action: { event: { name: "submit", context: {}, completionText: "ok" } } },
                { id: "main-col", component: "Column", children: { explicitList: ["q", "approve"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "hr", title: "Approval", body: "Deploy prod", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-hr", surface: hilSurface, promptState: "resolved" }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByText("Approval")).toBeInTheDocument();
        expect(screen.queryByText("Needs your input")).not.toBeInTheDocument();
        expect(screen.queryByText("Approve the deploy?")).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /approve/i })).not.toBeInTheDocument();
        expect(screen.getByText("Expired")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: /expand details/i }));
        expect(await screen.findByText("This request is no longer awaiting input — it expired or was already answered.")).toBeInTheDocument();
    });

    it("renders a HIL prompt with promptState 'pending' as an answerable card", async () => {
        const hilSurface = {
            surfaceId: "req-hp",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                { id: "q", component: "Text", text: { literalString: "Approve the deploy?" }, variant: "body" },
                { id: "lbl", component: "Text", text: { literalString: "Approve" } },
                { id: "approve", component: "Button", variant: "primary", child: "lbl", action: { event: { name: "submit", context: {}, completionText: "ok" } } },
                { id: "main-col", component: "Column", children: { explicitList: ["q", "approve"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "hp", title: "Approval", body: "", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-hp", surface: hilSurface, promptState: "pending" }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByText("Needs response soon")).toBeInTheDocument();
        await expandFirstRow();
        expect(await screen.findByText("Approve the deploy?")).toBeInTheDocument();
        expect(screen.queryByText("Expired")).not.toBeInTheDocument();
    });

    it("keeps an input request answerable after it is marked read", async () => {
        const hilSurface = {
            surfaceId: "req-hm",
            catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
            components: [
                { id: "q", component: "Text", text: { literalString: "Approve the deploy?" }, variant: "body" },
                { id: "lbl", component: "Text", text: { literalString: "Approve" } },
                { id: "approve", component: "Button", variant: "primary", child: "lbl", action: { event: { name: "submit", context: {}, completionText: "ok" } } },
                { id: "main-col", component: "Column", children: { explicitList: ["q", "approve"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
            dataModel: {},
        };
        let status = "unread";
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "hm", title: "Approval", body: "", status, createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-hm", surface: hilSurface, promptState: "pending" }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        mockPost.mockImplementation((url: string) => {
            if (url === "/api/v1/notify/inbox/hm/read") status = "read";
            return Promise.resolve(undefined);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        const user = userEvent.setup();
        await user.click(await screen.findByRole("button", { name: /notification actions/i }));
        await user.click(await screen.findByRole("menuitem", { name: /mark as read/i }));
        await user.click(await screen.findByRole("button", { name: /notification actions/i }));
        expect(await screen.findByRole("menuitem", { name: /mark as unread/i })).toBeInTheDocument();
        await user.keyboard("{Escape}");

        expect(screen.getByText("Needs response soon")).toBeInTheDocument();
        await expandFirstRow();
        expect(await screen.findByText("Approve the deploy?")).toBeInTheDocument();
    });

    it("shows an empty state when the inbox has no notifications", async () => {
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox")) return Promise.resolve({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 0, count: 0, nextPage: null } } });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText(/no notifications yet/i)).toBeInTheDocument();
    });

    it("marks a notification read when its row is expanded (clears the unread bar)", async () => {
        let n1Read = false;
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "n1", title: "Report ready", body: "Your report is ready.", status: n1Read ? "read" : "unread", createdAt: "2026-06-01T10:00:00.000Z" }],
                    meta: { pagination: { pageNumber: 1, pageSize: 10, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        mockPost.mockImplementation((url: string) => {
            if (url.includes("/n1/read")) n1Read = true;
            return Promise.resolve(undefined);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: /expand details/i }));
        await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/n1/read"));
    });

    it("offers 'Mark as unread' on an already-read row and posts to the unread endpoint", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText("Earlier note")).toBeInTheDocument();

        const user = userEvent.setup();
        await user.click(screen.getAllByRole("button", { name: /notification actions/i })[1]);
        await user.click(await screen.findByRole("menuitem", { name: /mark as unread/i }));
        await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/n2/unread"));
    });

    it("deletes a single notification from the row menu", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText("Report ready")).toBeInTheDocument();

        const user = userEvent.setup();
        await user.click(screen.getAllByRole("button", { name: /notification actions/i })[0]);
        await user.click(await screen.findByRole("menuitem", { name: /^delete$/i }));
        await waitFor(() => expect(mockDelete).toHaveBeenCalledWith("/api/v1/notify/inbox/n1"));
    });

    it("does not reopen a row's menu when the row leaves the list and comes back", async () => {
        let rows = inboxResponse.data;
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox")) return Promise.resolve({ ...inboxResponse, data: rows });
            if (url.includes("/notify/preferences")) return Promise.resolve(prefsResponse);
            return Promise.resolve({});
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText("Earlier note")).toBeInTheDocument();

        const user = userEvent.setup();
        await user.click(screen.getAllByRole("button", { name: /notification actions/i })[1]);
        expect(await screen.findByRole("menu")).toBeInTheDocument();

        rows = inboxResponse.data.filter(n => n.id !== "n2");
        await act(() => queryClient.invalidateQueries({ queryKey: notificationKeys.all }));
        await waitFor(() => expect(screen.queryByText("Earlier note")).toBeNull());

        rows = inboxResponse.data;
        await act(() => queryClient.invalidateQueries({ queryKey: notificationKeys.all }));
        expect(await screen.findByText("Earlier note")).toBeInTheDocument();
        expect(screen.queryByRole("menu")).toBeNull();
    });

    it("does not reopen a row's menu after a failed refresh recovers", async () => {
        let failInbox = false;
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox")) return failInbox ? Promise.reject(new Error("unavailable")) : Promise.resolve(inboxResponse);
            if (url.includes("/notify/preferences")) return Promise.resolve(prefsResponse);
            return Promise.resolve({});
        });
        queryClient.setQueryDefaults(notificationKeys.all, { retry: false });
        onTestFinished(() => queryClient.setQueryDefaults(notificationKeys.all, {}));
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText("Earlier note")).toBeInTheDocument();

        const user = userEvent.setup();
        await user.click(screen.getAllByRole("button", { name: /notification actions/i })[1]);
        expect(await screen.findByRole("menu")).toBeInTheDocument();

        failInbox = true;
        await act(() => queryClient.invalidateQueries({ queryKey: notificationKeys.all }));
        expect(await screen.findByText("Failed to load notifications.")).toBeInTheDocument();

        failInbox = false;
        await act(() => queryClient.invalidateQueries({ queryKey: notificationKeys.all }));
        expect(await screen.findByText("Earlier note")).toBeInTheDocument();
        expect(screen.queryByRole("menu")).toBeNull();
    });

    it("offers no way to clear every notification at once", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText("Report ready")).toBeInTheDocument();

        expect(screen.queryByRole("button", { name: /clear all/i })).not.toBeInTheDocument();
    });

    it("shows each row's time day first with a lowercase am or pm", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByText(formatNotificationTime("2026-06-01T10:00:00.000Z"))).toBeInTheDocument();
    });

    it("refreshes the list from the header", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText("Report ready")).toBeInTheDocument();
        const inboxGetsBefore = mockGet.mock.calls.filter(c => String(c[0]).includes("/notify/inbox")).length;

        fireEvent.click(screen.getByRole("button", { name: "Refresh" }));

        await waitFor(() => expect(mockGet.mock.calls.filter(c => String(c[0]).includes("/notify/inbox")).length).toBeGreaterThan(inboxGetsBefore));
    });

    it("opens the notification settings from the header", async () => {
        const openNotificationSettings = vi.fn();
        renderWithProviders(
            <Routes>
                <Route element={<Outlet context={{ openNotificationSettings }} />}>
                    <Route path="*" element={<NotificationsPage />} />
                </Route>
            </Routes>,
            { featureFlags: { notify_service: true }, configContextValues: {} }
        );

        fireEvent.click(await screen.findByRole("button", { name: "Notification Settings" }));

        expect(openNotificationSettings).toHaveBeenCalledTimes(1);
    });

    it("has no settings button where the app shell offers no way to open them", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText("Report ready")).toBeInTheDocument();

        expect(screen.queryByRole("button", { name: "Notification Settings" })).not.toBeInTheDocument();
    });

    it("paginates forward without bouncing back to page 1", async () => {
        const PAGE = 25;
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox")) {
                const pageNumber = Number(url.match(/pageNumber=(\d+)/)?.[1] ?? 1);
                const rows = Array.from({ length: PAGE }, (_, i) => ({
                    id: `p${pageNumber}-${i}`,
                    title: `Page${pageNumber}-${i + 1}`,
                    body: "",
                    status: "read",
                    createdAt: "2026-06-01T10:00:00.000Z",
                }));
                return Promise.resolve({ data: rows, meta: { pagination: { pageNumber, pageSize: PAGE, totalPages: 3, count: 60, nextPage: pageNumber < 3 ? pageNumber + 1 : null } } });
            }
            if (url.includes("/notify/preferences")) return Promise.resolve(prefsResponse);
            return Promise.resolve({});
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByText("Page1-1")).toBeInTheDocument();
        fireEvent.click(screen.getByText("Next"));
        expect(await screen.findByText("Page2-1")).toBeInTheDocument();
        expect(screen.queryByText("Page1-1")).not.toBeInTheDocument();
    });

    it("renders a deep-link CTA and opens the source session on click", async () => {
        const mockSwitch = vi.fn().mockResolvedValue(undefined);
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [
                        {
                            id: "s1",
                            title: "Scheduled task: daily-report",
                            body: "Status: completed",
                            status: "unread",
                            createdAt: "2026-06-01T10:00:00.000Z",
                            action: { label: "View result", sessionId: "sess-9", agentId: "Reporter" },
                        },
                    ],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {}, chatContextValues: { handleSwitchSession: mockSwitch } });

        const cta = await screen.findByRole("button", { name: "View Chat" });
        fireEvent.click(cta);

        await waitFor(() => expect(mockSwitch).toHaveBeenCalledWith("sess-9"));
        expect(mockNavigate).toHaveBeenCalledWith("/chat");
    });

    it("shows no CTA for a notification without an action", async () => {
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByText("Report ready")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "View Chat" })).not.toBeInTheDocument();
    });

    it("opens the shared-chat view from a share notification's CTA", async () => {
        const mockSwitch = vi.fn().mockResolvedValue(undefined);
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox")) return Promise.resolve(shareInboxResponse);
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {}, chatContextValues: { handleSwitchSession: mockSwitch } });

        fireEvent.click(await screen.findByRole("button", { name: "View Chat" }));

        expect(mockNavigate).toHaveBeenCalledWith("/shared-chat/share-9");
        expect(mockSwitch).not.toHaveBeenCalled();
    });

    it("opens the live session for an editor share that carries both references and marks the share viewed", async () => {
        const mockSwitch = vi.fn().mockResolvedValue(undefined);
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ ...shareInboxResponse.data[0], action: { label: "View shared chat", sessionId: "sess-9", shareId: "share-9" } }],
                    meta: shareInboxResponse.meta,
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {}, chatContextValues: { handleSwitchSession: mockSwitch } });

        fireEvent.click(await screen.findByRole("button", { name: "View Chat" }));

        await waitFor(() => expect(mockSwitch).toHaveBeenCalledWith("sess-9"));
        expect(mockNavigate).toHaveBeenCalledWith("/chat");
        expect(mockNavigate).not.toHaveBeenCalledWith("/shared-chat/share-9");
        expect(mockMarkShareViewed).toHaveBeenCalledWith("share-9");
    });

    it("navigates to the in-app path from a path notification's CTA", async () => {
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ ...shareInboxResponse.data[0], action: { label: "Review budget", path: "/budgets/b-1/usage" } }],
                    meta: shareInboxResponse.meta,
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: "Review budget" }));
        expect(screen.queryByRole("button", { name: "View Chat" })).not.toBeInTheDocument();

        expect(mockNavigate).toHaveBeenCalledWith("/budgets/b-1/usage");
        await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/sh1/read"));
    });

    it("shows no CTA when the action carries neither a session nor a share", async () => {
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ ...shareInboxResponse.data[0], action: { label: "Go to session" } }],
                    meta: shareInboxResponse.meta,
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByText("owner@example.com shared a chat with you")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "View Chat" })).not.toBeInTheDocument();
    });

    it("renders a URL in the expanded body as a link", async () => {
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox")) return Promise.resolve(shareInboxResponse);
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: /expand details/i }));

        const link = await screen.findByRole("link", { name: shareURL });
        expect(link).toHaveAttribute("href", shareURL);
        expect(link).toHaveAttribute("target", "_blank");
        expect(link).toHaveAttribute("rel", "noopener noreferrer");
    });

    const serveBody = (item: Record<string, unknown>) =>
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "md1", title: "Sample Markdown Generator completed", status: "read", createdAt: "2026-06-01T10:00:00.000Z", ...item }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });

    it("renders the expanded body as Markdown", async () => {
        serveBody({ body: "I've created `sample.md`:\n\n- **Headers**: levels 1\u20136\n- **Tables**: aligned\n\nSee [the docs](https://docs.example.com)." });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();

        expect(await screen.findByText("Headers", { selector: "strong" })).toBeInTheDocument();
        expect(screen.getAllByRole("listitem")).toHaveLength(2);
        expect(screen.getByText("sample.md", { selector: "code" })).toBeInTheDocument();
        expect(screen.queryByText(/\*\*Headers\*\*/)).not.toBeInTheDocument();
        const link = screen.getByRole("link", { name: "the docs" });
        expect(link).toHaveAttribute("href", "https://docs.example.com");
        expect(link).toHaveAttribute("rel", "noopener noreferrer");
    });

    it("does not highlight the expanded detail row on hover", async () => {
        serveBody({ body: "Plain details." });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();

        const detailRow = (await screen.findByText("Plain details.")).closest("tr");
        expect(detailRow).toHaveClass("hover:bg-transparent");
    });

    it("styles a table in the body like the builder tables", async () => {
        serveBody({ body: "| Metric | Value |\n| --- | --- |\n| Total tasks | 2 |" });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();

        const body = (await screen.findByRole("columnheader", { name: "Metric" })).closest("div");
        expect(body).toHaveClass("[&_th]:border-0!", "[&_th]:text-(--primary-text-wMain)", "[&_td]:border-b!");
    });

    it("offers View Chat as a text button without the link underline", async () => {
        mockGet.mockImplementation((url: string) => {
            if (url.includes("/notify/inbox"))
                return Promise.resolve({
                    data: [{ id: "s1", title: "Scheduled task: daily-report", body: "Done", status: "read", createdAt: "2026-06-01T10:00:00.000Z", action: { label: "View result", sessionId: "sess-9" } }],
                    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
                });
            return Promise.resolve(prefsResponse);
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        const cta = await screen.findByRole("button", { name: "View Chat" });

        expect(cta.className).toContain("hover:bg-(--primary-w10)");
        expect(cta.className).not.toContain("underline");
        expect(cta.className).toContain("h-7");
        expect(cta.className).not.toContain("h-8");
    });

    it("shows HTML in a body as text and renders no image, form, style or script from it", async () => {
        serveBody({
            body: 'Expected <name> in config, got <nil>.\n\n![](https://evil.example/p.gif)\n\n<img src=x onerror="alert(1)"><div style="position:fixed;inset:0">cover</div><form action="https://evil.example"><input type="password"></form><script>alert(2)</script>\n\n[click](javascript:alert(3))',
        });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();

        expect(await screen.findByText(/Expected <name> in config, got <nil>\./)).toBeInTheDocument();
        expect(screen.getByText(/<script>alert\(2\)<\/script>/)).toBeInTheDocument();
        for (const selector of ["img", "form", "input[type=password]", "script", "[style*='position']"]) {
            expect(document.querySelector(selector), selector).toBeNull();
        }
        expect(screen.getByText("click").closest("a")?.getAttribute("href") ?? "").not.toMatch(/^javascript:/i);
    });

    it("shows a share notification's body as plain text with its link", async () => {
        serveBody({ category: "chat_share", source: "chat_share", body: `"[Verify your account](https://evil.example/login)"\n${shareURL}` });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();

        expect(await screen.findByText(/\[Verify your account\]\(/)).toBeInTheDocument();
        expect(screen.queryByRole("link", { name: "Verify your account" })).not.toBeInTheDocument();
        expect(screen.getByRole("link", { name: shareURL })).toHaveAttribute("href", shareURL);
    });

    it("says there are no details when the body is blank", async () => {
        serveBody({ body: "  \n " });
        renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {} });

        await expandFirstRow();

        expect(await screen.findByText("No additional details.")).toBeInTheDocument();
    });

    describe("notification categories", () => {
        const categoriesResponse = {
            data: [
                { category: "scheduled_task", label: "Scheduled task results", enabled: true },
                { category: "agent_message", label: "Agent & workflow updates", enabled: false },
                { category: "hil", label: "Approvals & input requests", enabled: true },
            ],
            meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 3, nextPage: null } },
        };
        const categorizedInbox = {
            data: [
                { id: "c1", title: "Nightly report", body: "", status: "unread", category: "scheduled_task", createdAt: "2026-06-01T10:00:00.000Z" },
                { id: "c2", title: "Approval answered", body: "", status: "read", category: "hil", createdAt: "2026-06-01T09:00:00.000Z" },
                { id: "c3", title: "Plain note", body: "", status: "read", createdAt: "2026-06-01T08:00:00.000Z" },
                { id: "c4", title: "Agent update", body: "", status: "read", category: "agent_message", createdAt: "2026-06-01T07:00:00.000Z" },
            ],
            meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 4, nextPage: null } },
        };
        const addNotification = vi.fn();

        beforeEach(() => {
            addNotification.mockReset();
            mockGet.mockImplementation((url: string) => {
                if (url.includes("/notify/inbox")) return Promise.resolve(categorizedInbox);
                if (url.includes("/notify/preferences/categories")) return Promise.resolve(categoriesResponse);
                return Promise.resolve(prefsResponse);
            });
            mockPut.mockResolvedValue({ category: "scheduled_task", label: "Scheduled task results", enabled: false });
        });

        const renderPage = () => renderWithProviders(<NotificationsPage />, { featureFlags: { notify_service: true }, configContextValues: {}, chatContextValues: { addNotification } });

        const openRowMenu = async (title: string) => {
            const row = (await screen.findByText(title)).closest("tr");
            if (!row) throw new Error(`no table row for ${title}`);
            await userEvent.setup().click(within(row).getByRole("button", { name: /notification actions/i }));
        };

        it("shows each categorized row's category in its own column", async () => {
            renderPage();

            expect(await screen.findByText("Scheduled task results")).toBeInTheDocument();
            expect(screen.getByText("Approvals & input requests")).toBeInTheDocument();
            const plainRow = screen.getByText("Plain note").closest("tr");
            if (!plainRow) throw new Error("no table row for Plain note");
            expect(within(plainRow).queryByText(/results|updates|requests/i)).not.toBeInTheDocument();
        });

        it("turns off a category from the row menu and confirms with a toast", async () => {
            renderPage();
            await screen.findByText("Scheduled task results");

            await openRowMenu("Nightly report");
            await userEvent.setup().click(await screen.findByRole("menuitem", { name: "Turn off “Scheduled task results” notifications" }));

            await waitFor(() => expect(mockPut).toHaveBeenCalledWith("/api/v1/notify/preferences/categories", { category: "scheduled_task", enabled: false }));
            await waitFor(() => expect(addNotification).toHaveBeenCalledWith("“Scheduled task results” notifications are turned off. You can turn them back on in Notification Settings.", "success"));
        });

        it("warns when turning off a category fails", async () => {
            mockPut.mockRejectedValue(new Error("boom"));
            renderPage();
            await screen.findByText("Scheduled task results");

            await openRowMenu("Nightly report");
            await userEvent.setup().click(await screen.findByRole("menuitem", { name: "Turn off “Scheduled task results” notifications" }));

            await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Couldn't turn off “Scheduled task results” notifications. Try again from Notification Settings.", "warning"));
        });

        it("separates only Delete in the row menu", async () => {
            renderPage();
            await screen.findByText("Scheduled task results");

            await openRowMenu("Nightly report");

            const menu = await screen.findByRole("menu");
            expect(within(menu).getAllByRole("separator")).toHaveLength(1);
        });

        const lastInboxRequest = () =>
            mockGet.mock.calls
                .map(c => String(c[0]))
                .filter(u => u.includes("/notify/inbox"))
                .at(-1);

        const chooseCategory = async (name: string) => {
            const user = userEvent.setup();
            await user.click(screen.getByRole("combobox", { name: "Category" }));
            await user.click(await screen.findByRole("option", { name }));
        };

        it("filters the list by category and starts again from the first page", async () => {
            mockGet.mockImplementation((url: string) => {
                if (url.includes("/notify/inbox")) return Promise.resolve({ ...categorizedInbox, meta: { pagination: { pageNumber: 1, pageSize: 25, totalPages: 3, count: 60, nextPage: 2 } } });
                if (url.includes("/notify/preferences/categories")) return Promise.resolve(categoriesResponse);
                return Promise.resolve(prefsResponse);
            });
            renderPage();
            await screen.findByText("Nightly report");
            fireEvent.click(screen.getByText("Next"));
            await waitFor(() => expect(lastInboxRequest()).toMatch(/pageNumber=2&pageSize=25$/));

            await chooseCategory("Scheduled task results");

            await waitFor(() => expect(lastInboxRequest()).toMatch(/\/notify\/inbox\?pageNumber=1&pageSize=25&category=scheduled_task$/));
        });

        it("drops the category filter when All categories is chosen again", async () => {
            renderPage();
            await screen.findByText("Nightly report");

            await chooseCategory("Scheduled task results");
            await waitFor(() => expect(lastInboxRequest()).toMatch(/category=scheduled_task$/));
            await chooseCategory("All categories");

            await waitFor(() => expect(screen.getByRole("combobox", { name: "Category" })).toHaveTextContent("All categories"));
            expect(await screen.findByText("Plain note")).toBeInTheDocument();
            expect(mockGet.mock.calls.map(c => String(c[0])).filter(u => u.includes("category=all"))).toEqual([]);
        });

        it("combines the unread toggle with a category", async () => {
            mockGet.mockImplementation((url: string) => {
                if (url.includes("/notify/inbox") && url.includes("status=unread")) return Promise.resolve({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 25, totalPages: 1, count: 0, nextPage: null } } });
                if (url.includes("/notify/inbox")) return Promise.resolve(categorizedInbox);
                if (url.includes("/notify/preferences/categories")) return Promise.resolve(categoriesResponse);
                return Promise.resolve(prefsResponse);
            });
            renderPage();
            await screen.findByText("Nightly report");

            fireEvent.click(screen.getByRole("switch", { name: "Show only unread notifications" }));
            await chooseCategory("Scheduled task results");

            await waitFor(() => expect(lastInboxRequest()).toMatch(/\/notify\/inbox\?pageNumber=1&pageSize=25&status=unread&category=scheduled_task$/));
            expect(await screen.findByText("No unread notifications of this type.")).toBeInTheDocument();
        });

        it("marks read only the notifications of the chosen category", async () => {
            renderPage();
            await screen.findByText("Nightly report");

            await chooseCategory("Scheduled task results");
            fireEvent.click(screen.getByRole("button", { name: "Mark all as read" }));

            await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/read-all?category=scheduled_task"));
        });

        it("marks every notification read when no category is chosen", async () => {
            renderPage();
            await screen.findByText("Nightly report");

            fireEvent.click(screen.getByRole("button", { name: "Mark all as read" }));

            await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/inbox/read-all"));
        });

        it("drops the filters when the bell links to a notification", async () => {
            renderWithProviders(
                <>
                    <NotificationsPage />
                    <Link to="/?focus=c4">Open from the bell</Link>
                </>,
                { featureFlags: { notify_service: true }, configContextValues: {}, chatContextValues: { addNotification } }
            );
            await screen.findByText("Nightly report");
            fireEvent.click(screen.getByRole("switch", { name: "Show only unread notifications" }));
            await chooseCategory("Scheduled task results");
            await waitFor(() => expect(lastInboxRequest()).toMatch(/status=unread&category=scheduled_task$/));

            fireEvent.click(screen.getByRole("link", { name: "Open from the bell" }));

            await waitFor(() => expect(screen.getByRole("combobox", { name: "Category" })).toHaveTextContent("All categories"));
            expect(screen.getByRole("switch", { name: "Show only unread notifications" })).not.toBeChecked();
            expect(await screen.findByText("Agent update")).toBeInTheDocument();
        });

        it.each([
            ["an approval, which always reaches the inbox", "Approval answered"],
            ["an uncategorized notification", "Plain note"],
            ["a category that is already off", "Agent update"],
        ])("hides the turn-off action for %s", async (_, title) => {
            renderPage();
            await screen.findByText("Scheduled task results");

            await openRowMenu(title);
            expect(await screen.findByRole("menuitem", { name: /delete/i })).toBeInTheDocument();
            expect(screen.queryByRole("menuitem", { name: /turn off/i })).not.toBeInTheDocument();
        });
    });
});
