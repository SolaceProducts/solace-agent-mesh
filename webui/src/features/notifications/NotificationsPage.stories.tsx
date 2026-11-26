// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { Outlet, Route, Routes } from "react-router-dom";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { NotificationsPage } from "@/features/notifications/NotificationsPage";

import { withDefaults } from "@/testing/helpers";

const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();

const approvalSurface = {
    surfaceId: "req-story",
    catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
    components: [
        { id: "q", component: "Text", text: { literalString: "Which scope should the report cover?" }, variant: "body" },
        { id: "approve-lbl", component: "Text", text: { literalString: "Tenant-wide" } },
        { id: "approve", component: "Button", variant: "primary", child: "approve-lbl", action: { event: { name: "submit", context: { scope: "tenant" } } } },
        { id: "main-col", component: "Column", children: { explicitList: ["q", "approve"] } },
        { id: "root", component: "Card", child: "main-col" },
    ],
    dataModel: {},
};

const inbox = {
    data: [
        { id: "n1", title: "Your input is requested", body: "", status: "unread", category: "hil", createdAt: minutesAgo(2), requestId: "req-story", surface: approvalSurface, promptState: "pending" },
        { id: "n2", title: "Nightly data sync completed", body: "All 14 tables synced.", status: "unread", category: "scheduled_task", createdAt: minutesAgo(30), action: { label: "View result", sessionId: "sess-1" } },
        { id: "n3", title: "Alice shared a chat with you", body: '"Quarterly plan"', status: "unread", category: "chat_share", createdAt: minutesAgo(90), action: { label: "View shared chat", shareId: "share-1" } },
        { id: "n4", title: "Research agent found 3 relevant papers", body: "", status: "read", category: "agent_message", createdAt: minutesAgo(600) },
        { id: "n5", title: "Weekly sales report failed", body: "The data source did not answer.", status: "read", category: "scheduled_task", createdAt: minutesAgo(3000) },
    ],
    meta: { pagination: { pageNumber: 1, pageSize: 25, totalPages: 1, count: 5, nextPage: null } },
};

const categories = {
    data: [
        { category: "scheduled_task", label: "Scheduled task results", enabled: true },
        { category: "agent_message", label: "Messages from agents and workflows", enabled: true },
        { category: "hil", label: "Tool approvals and agent input requests", enabled: true },
        { category: "chat_share", label: "Chats shared with me", enabled: true },
    ],
    meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 4, nextPage: null } },
};

const handlers = [http.get("*/api/v1/notify/inbox", () => HttpResponse.json(inbox)), http.get("*/api/v1/notify/preferences/categories", () => HttpResponse.json(categories))];

const meta = {
    title: "Notifications/NotificationsPage",
    component: NotificationsPage,
    render: () => (
        <Routes>
            <Route element={<Outlet context={{ openNotificationSettings: () => {} }} />}>
                <Route path="*" element={<NotificationsPage />} />
            </Route>
        </Routes>
    ),
    parameters: {
        layout: "fullscreen",
        msw: { handlers: withDefaults(...handlers) },
        featureContext: { notify_service: true },
    },
} satisfies Meta<typeof NotificationsPage>;

export default meta;

const longNote = "A long explanatory value that has to wrap inside its cell instead of running into the next column of the table";

const reportBody = [
    "Yesterday was a light day: 2 tasks, both completed.",
    "",
    "## Key metrics",
    "",
    "| Metric | Value |",
    "| --- | --- |",
    "| Total tasks | 2 |",
    "| Completed | 2 (100% success) |",
    "| Failed | 0 (0% failure) |",
    `| Note | ${longNote} |`,
].join("\n");

const reportInbox = { ...inbox, data: [{ ...inbox.data[1], title: "Scheduled task: Daily Report Generation", body: reportBody }], meta: { pagination: { ...inbox.meta.pagination, count: 1 } } };

const colorOf = (element: Element) => getComputedStyle(element).color;

export const ExpandedReportWithTable = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v1/notify/inbox", () => HttpResponse.json(reportInbox)),
                http.get("*/api/v1/notify/preferences/categories", () => HttpResponse.json(categories))
            ),
        },
    },
    play: async () => {
        const inboxTable = await screen.findByRole("table");
        await within(inboxTable).findByText("Scheduled task: Daily Report Generation");

        await userEvent.click(within(inboxTable).getByRole("button", { name: "Expand details" }));
        const heading = await screen.findByRole("columnheader", { name: "Metric" });
        const reportTable = within(heading.closest("table") as HTMLElement);
        expect(colorOf(heading)).toBe(colorOf(within(inboxTable).getByRole("columnheader", { name: "Title" })));
        expect(getComputedStyle(heading).borderLeftWidth).toBe("0px");
        expect(getComputedStyle(heading).borderBottomWidth).toBe("1px");

        const middleRowCell = reportTable.getByRole("cell", { name: "Total tasks" });
        expect(getComputedStyle(middleRowCell).borderLeftWidth).toBe("0px");
        expect(getComputedStyle(middleRowCell).borderBottomWidth).toBe("1px");

        const lastRowCell = reportTable.getByRole("cell", { name: longNote });
        expect(getComputedStyle(lastRowCell).borderBottomWidth).toBe("0px");
        expect(getComputedStyle(lastRowCell).whiteSpace).not.toBe("nowrap");
    },
};

export const Default = {
    play: async () => {
        const table = await screen.findByRole("table");
        const rows = within(table);
        await rows.findByText("Nightly data sync completed");
        expect(rows.getByRole("columnheader", { name: "Category" })).toBeInTheDocument();
        expect(rows.getByText("Needs response soon")).toBeInTheDocument();
        expect(rows.getAllByRole("button", { name: "View Chat" })).toHaveLength(2);
        expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Notification Settings" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /clear all/i })).toBeNull();
    },
};

export const OneRowMenuAtATime = {
    play: async () => {
        const table = await screen.findByRole("table");
        await within(table).findByText("Nightly data sync completed");
        const triggers = within(table).getAllByRole("button", { name: "Notification actions" });

        await userEvent.click(triggers[0]);
        expect(within(await screen.findByRole("menu")).getByRole("menuitem", { name: "Mark as read" })).toBeInTheDocument();
        expect(document.body).not.toHaveAttribute("data-scroll-locked");
        expect(getComputedStyle(document.body).overflow).not.toBe("hidden");

        await userEvent.click(triggers[3]);
        await waitFor(() => expect(screen.getAllByRole("menu")).toHaveLength(1));
        expect(within(screen.getByRole("menu")).getByRole("menuitem", { name: "Mark as unread" })).toBeInTheDocument();

        await userEvent.keyboard("{Escape}");
        await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());

        await userEvent.click(triggers[3]);
        await userEvent.click(within(await screen.findByRole("menu")).getByRole("menuitem", { name: "Mark as unread" }));
        await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());

        await userEvent.click(triggers[3]);
        expect(await screen.findByRole("menu")).toBeInTheDocument();
    },
};

const lit = (text: string) => ({ literalString: text });
const catalogId = "https://a2ui.org/specification/v0_9/basic_catalog.json";

const choiceQuestion = {
    surfaceId: "req-choice",
    catalogId,
    components: [
        { id: "msg", component: "Text", text: lit("Before preparing the weekly sales summary, please choose the region it should cover."), variant: "body" },
        { id: "q0-label", component: "Text", text: lit("Which region should the weekly sales summary cover?"), variant: "h3" },
        {
            id: "q0-picker",
            component: "ChoicePicker",
            variant: "mutuallyExclusive",
            options: [
                { label: lit("EMEA"), value: "emea" },
                { label: lit("APAC"), value: "apac" },
            ],
            value: { path: "/answers/q0" },
        },
        { id: "q0-other", component: "TextField", label: lit("Other") },
        { id: "submit-text", component: "Text", text: lit("Submit") },
        { id: "submit-btn", component: "Button", variant: "primary", child: "submit-text", action: { event: { name: "submit" } } },
        { id: "actions", component: "Row", children: { explicitList: ["submit-btn"] } },
        { id: "main-col", component: "Column", children: { explicitList: ["msg", "q0-label", "q0-picker", "q0-other", "actions"] } },
        { id: "root", component: "Card", child: "main-col" },
    ],
    dataModel: {},
};

const tabbedQuestions = {
    surfaceId: "req-tabs",
    catalogId,
    components: [
        { id: "msg", component: "Text", text: lit("A couple of questions before I build the report."), variant: "body" },
        { id: "q0-label", component: "Text", text: lit("Which region?"), variant: "h3" },
        { id: "q0-picker", component: "ChoicePicker", variant: "mutuallyExclusive", options: [{ label: lit("EMEA"), value: "emea" }], value: { path: "/answers/q0" } },
        { id: "q0-panel", component: "Column", children: { explicitList: ["q0-label", "q0-picker"] } },
        { id: "q1-label", component: "Text", text: lit("What should the report be called?"), variant: "h3" },
        { id: "q1-text", component: "TextField", label: lit("Title"), value: { path: "/answers/q1" } },
        { id: "q1-panel", component: "Column", children: { explicitList: ["q1-label", "q1-text"] } },
        {
            id: "question-tabs",
            component: "Tabs",
            tabItems: [
                { title: lit("Region"), child: "q0-panel" },
                { title: lit("Title"), child: "q1-panel" },
            ],
        },
        { id: "main-col", component: "Column", children: { explicitList: ["msg", "question-tabs"] } },
        { id: "root", component: "Card", child: "main-col" },
    ],
    dataModel: {},
};

const approvalWithArgs = {
    surfaceId: "req-args",
    catalogId,
    components: [
        { id: "warn-icon", component: "Icon", icon: "warning" },
        { id: "title", component: "Text", text: lit("Approval Required"), variant: "h3" },
        { id: "title-row", component: "Row", children: { explicitList: ["warn-icon", "title"] } },
        { id: "info", component: "Text", text: lit('Agent "OpsAgent" wants to run tool: restart_service'), variant: "body" },
        { id: "arg-service-label", component: "Text", text: lit("service"), variant: "caption" },
        { id: "arg-service-value", component: "Text", text: lit("billing-api"), variant: "body", multiline: true },
        { id: "arg-service", component: "Column", children: { explicitList: ["arg-service-label", "arg-service-value"] } },
        { id: "args-fields", component: "Column", children: { explicitList: ["arg-service"] } },
        { id: "args-card", component: "Card", child: "args-fields" },
        { id: "approve-text", component: "Text", text: lit("Approve") },
        { id: "approve-btn", component: "Button", variant: "primary", child: "approve-text", action: { event: { name: "submit", context: { approved: true } } } },
        { id: "actions", component: "Row", children: { explicitList: ["approve-btn"] } },
        { id: "countdown", component: "Text", text: lit("Expires in 45 minutes"), variant: "caption" },
        { id: "main-col", component: "Column", children: { explicitList: ["title-row", "info", "args-card", "actions", "countdown"] } },
        { id: "root", component: "Card", child: "main-col" },
    ],
    dataModel: {},
};

const volumePrompt = {
    surfaceId: "req-volume",
    catalogId,
    components: [
        { id: "title", component: "Text", text: lit("Volume Required"), variant: "h3" },
        { id: "prompt", component: "Text", text: lit("Pick a volume to mount for the analysis."), variant: "body" },
        { id: "volume-picker", component: "ChoicePicker", variant: "mutuallyExclusive", options: [{ label: lit("datasets"), value: "v1" }], value: { path: "/selected_volume" } },
        { id: "main-col", component: "Column", children: { explicitList: ["title", "prompt", "volume-picker"] } },
        { id: "root", component: "Card", child: "main-col" },
    ],
    dataModel: {},
};

const waiting = (id: string, surface: object, minutes: number) => ({
    id,
    title: "Your input is requested",
    body: "",
    status: "unread",
    category: "hil",
    createdAt: minutesAgo(minutes),
    requestId: `req-${id}`,
    surface,
    promptState: "pending",
    action: { label: "", sessionId: "chat-sales" },
});

const waitingInbox = {
    data: [waiting("w1", choiceQuestion, 1), waiting("w2", tabbedQuestions, 2), waiting("w3", approvalWithArgs, 3), waiting("w4", volumePrompt, 4), { ...inbox.data[1], body: "All 14 tables synced in 3 minutes." }],
    meta: { pagination: { pageNumber: 1, pageSize: 25, totalPages: 1, count: 5, nextPage: null } },
};

export const WaitingRequestDetails = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v1/notify/inbox", () => HttpResponse.json(waitingInbox)),
                http.get("*/api/v1/notify/preferences/categories", () => HttpResponse.json(categories)),
                http.get("*/api/v1/sessions/:sessionId", () => HttpResponse.json({ id: "chat-sales", name: "Weekly Sales Summary" }))
            ),
        },
    },
    play: async () => {
        const table = await screen.findByRole("table");
        await within(table).findByText("Nightly data sync completed");
        await userEvent.click(within(table).getAllByRole("button", { name: "Expand details" })[0]);
        expect(await screen.findByText("Selection Required")).toBeInTheDocument();
        expect(await screen.findByText("Weekly Sales Summary")).toBeInTheDocument();
        expect(screen.queryByRole("radio")).toBeNull();
    },
};
