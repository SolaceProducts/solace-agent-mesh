// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import type { NotificationItem } from "@/api/notifications";

import { queryClient } from "@/providers/query/QueryClient";

import { mockDiscoveredAgentCardInfos } from "@/testing/data";
import { MockEventSource } from "@/testing/helpers/mockEventSource";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { MessageFE } from "@/types";

import { ChatPage } from "./ChatPage";

const OPEN_SESSION = "chat-1";
const INBOX_URL = "*/api/v1/notify/inbox";
const RESPOND_URL = "*/api/v1/notify/inbox/:id/respond";
const WELCOME_PROMPT = "Welcome to the example workspace";

const surface = {
    surfaceId: "req-1",
    catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
    components: [
        { id: "q", component: "Text", text: { literalString: "Approve the deploy?" }, variant: "body" },
        { id: "approve-lbl", component: "Text", text: { literalString: "Approve" } },
        { id: "approve", component: "Button", variant: "primary", child: "approve-lbl", action: { event: { name: "submit", context: { answers: { q0: "yes" } }, completionText: "Submitted" } } },
        { id: "main-col", component: "Column", children: { explicitList: ["q", "approve"] } },
        { id: "root", component: "Card", child: "main-col" },
    ],
    dataModel: {},
};

const approvalSurface = {
    surfaceId: "req-1",
    catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
    components: [
        { id: "info", component: "Text", text: { literalString: 'Agent "Helper" wants to run tool: deploy' }, variant: "body" },
        { id: "arg-env-label", component: "Text", text: { literalString: "env" }, variant: "caption" },
        { id: "arg-env-value", component: "Text", text: { literalString: "prod" }, variant: "body" },
        { id: "arg-env", component: "Column", children: { explicitList: ["arg-env-label", "arg-env-value"] } },
        { id: "approve-lbl", component: "Text", text: { literalString: "Approve" } },
        { id: "approve", component: "Button", variant: "primary", child: "approve-lbl", action: { event: { name: "submit", context: { approved: true }, completionText: "Approved" } } },
        { id: "deny-lbl", component: "Text", text: { literalString: "Deny" } },
        { id: "deny", component: "Button", child: "deny-lbl", action: { event: { name: "submit", context: { approved: false }, completionText: "Denied" } } },
        { id: "buttons", component: "Row", children: { explicitList: ["approve", "deny"] } },
        { id: "main-col", component: "Column", children: { explicitList: ["info", "arg-env", "buttons"] } },
        { id: "root", component: "Card", child: "main-col" },
    ],
    dataModel: {},
};

function pendingItem(overrides: Partial<NotificationItem> = {}): NotificationItem {
    return {
        id: "n1",
        title: "Approval needed",
        body: "",
        status: "unread",
        createdAt: "2026-06-01T10:00:00.000Z",
        requestId: "req-1",
        promptState: "pending",
        surface,
        action: { label: "", sessionId: OPEN_SESSION },
        ...overrides,
    } as NotificationItem;
}

function inboxPage(items: NotificationItem[]) {
    return { data: items, meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: items.length, nextPage: null } } };
}

let inboxItems: NotificationItem[] = [];
let inboxRequests = 0;
let respondRequests: { id: string; body: unknown }[] = [];

const server = setupServer(
    http.get(INBOX_URL, () => {
        inboxRequests += 1;
        return HttpResponse.json(inboxPage(inboxItems));
    }),
    http.post(RESPOND_URL, async ({ params, request }) => {
        respondRequests.push({ id: String(params.id), body: await request.json() });
        inboxItems = inboxItems.map(n => (n.id === params.id ? { ...n, promptState: "resolved" } : n));
        return new HttpResponse(null, { status: 204 });
    }),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 0, nextPage: null } } })),
    http.get("*/api/v1/sessions/:sessionId/contextUsage", () => new HttpResponse(null, { status: 404 })),
    http.get("*/api/v1/share/link/:sessionId", () => new HttpResponse(null, { status: 404 }))
);

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
beforeEach(() => {
    inboxItems = [];
    inboxRequests = 0;
    respondRequests = [];
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
    localStorage.clear();
    queryClient.clear();
});
afterEach(() => {
    server.resetHandlers();
    vi.unstubAllGlobals();
});
afterAll(() => server.close());

function renderChatPage({ notifyService = true, messages = [], displayError }: { notifyService?: boolean; messages?: MessageFE[]; displayError?: () => void } = {}) {
    return renderWithProviders(<ChatPage />, {
        featureFlags: { notify_service: notifyService },
        userContextValues: { username: null },
        chatContextValues: { sessionId: OPEN_SESSION, messages, isResponding: false, agents: mockDiscoveredAgentCardInfos, ...(displayError && { displayError }) },
        configContextValues: { persistenceEnabled: false, configWelcomeMessage: WELCOME_PROMPT },
    });
}

function liveRequestMessage(requestId: string): MessageFE {
    return {
        isUser: false,
        isComplete: true,
        parts: [],
        taskId: "task-1",
        userInputRequest: { requestId, expiresAt: "2099-01-01T00:00:00.000Z", source: "ask_user_question", surface, taskId: "task-1", agentName: "Helper" },
    } as MessageFE;
}

describe("ChatPage pending input requests", () => {
    test("shows an input request waiting in the open chat and not the welcome screen", async () => {
        inboxItems = [pendingItem()];
        renderChatPage();

        const pending = await screen.findByTestId("pending-chat-requests");
        expect(within(pending).getByText("Approve the deploy?")).toBeInTheDocument();
        expect(within(pending).getByRole("button", { name: "Approve" })).toBeInTheDocument();
        expect(within(pending).queryByText("Approval needed")).not.toBeInTheDocument();
        expect(screen.queryByText(WELCOME_PROMPT)).not.toBeInTheDocument();
    });

    test("shows the welcome screen when no input request is waiting", async () => {
        renderChatPage();

        await waitFor(() => expect(inboxRequests).toBeGreaterThan(0));
        expect(await screen.findByText(WELCOME_PROMPT)).toBeInTheDocument();
        expect(screen.queryByTestId("pending-chat-requests")).not.toBeInTheDocument();
    });

    test("ignores an input request waiting in another chat", async () => {
        inboxItems = [pendingItem({ action: { label: "", sessionId: "chat-2" } })];
        renderChatPage();

        await waitFor(() => expect(inboxRequests).toBeGreaterThan(0));
        await screen.findByTestId("chat-content-area");
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(screen.queryByTestId("pending-chat-requests")).not.toBeInTheDocument();
        expect(screen.queryByText("Approve the deploy?")).not.toBeInTheDocument();
    });

    test("does not show a second copy of a request the chat already shows from its own stream", async () => {
        inboxItems = [pendingItem()];
        renderChatPage({ messages: [liveRequestMessage("req-1")] });

        await waitFor(() => expect(inboxRequests).toBeGreaterThan(0));
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(screen.queryByTestId("pending-chat-requests")).not.toBeInTheDocument();
    });

    test("answers through the notification respond endpoint and keeps the answer in the chat once the inbox resolves it", async () => {
        inboxItems = [pendingItem()];
        renderChatPage();

        const pending = await screen.findByTestId("pending-chat-requests");
        await userEvent.click(within(pending).getByRole("button", { name: "Approve" }));

        await waitFor(() => expect(respondRequests).toEqual([{ id: "n1", body: { status: "answered", data: { answers: { q0: "yes" } } } }]));
        await waitFor(() => expect(inboxRequests).toBeGreaterThan(1));
        expect(await within(pending).findByText("yes")).toBeInTheDocument();
        expect(within(pending).queryByRole("button")).not.toBeInTheDocument();
        expect(screen.queryByText(WELCOME_PROMPT)).not.toBeInTheDocument();
    });

    test("a denied tool approval collapses to the same summary the chat's own card shows", async () => {
        inboxItems = [pendingItem({ surface: approvalSurface })];
        renderChatPage();

        const pending = await screen.findByTestId("pending-chat-requests");
        await userEvent.click(within(pending).getByRole("button", { name: "Deny" }));

        await waitFor(() => expect(respondRequests).toEqual([{ id: "n1", body: { status: "answered", data: { approved: false } } }]));
        await waitFor(() => expect(inboxRequests).toBeGreaterThan(1));
        expect(await within(pending).findByText("Denied")).toBeInTheDocument();
        expect(within(pending).getByText('Agent "Helper" wants to run tool: deploy')).toBeInTheDocument();
        expect(within(pending).getByText("prod")).toBeInTheDocument();
        expect(within(pending).queryByRole("button")).not.toBeInTheDocument();
    });

    test("reports a refused answer in the chat's error dialog with the server's message", async () => {
        inboxItems = [pendingItem()];
        server.use(http.post(RESPOND_URL, () => HttpResponse.json({ message: "This request is no longer awaiting input.", errorId: "e1" }, { status: 409 })));
        const displayError = vi.fn();
        renderChatPage({ displayError });

        const pending = await screen.findByTestId("pending-chat-requests");
        await userEvent.click(within(pending).getByRole("button", { name: "Approve" }));

        await waitFor(() => expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ title: "Failed to submit response", error: "This request is no longer awaiting input." })));
        expect(within(pending).getByRole("button", { name: "Approve" })).toBeInTheDocument();
    });

    test("never asks the notification inbox when the notification service is off", async () => {
        inboxItems = [pendingItem()];
        renderChatPage({ notifyService: false });

        await screen.findByTestId("chat-content-area");
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(inboxRequests).toBe(0);
        expect(screen.queryByTestId("pending-chat-requests")).not.toBeInTheDocument();
    });
});
