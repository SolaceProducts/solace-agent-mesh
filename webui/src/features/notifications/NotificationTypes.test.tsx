// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { notificationKeys } from "@/api/notifications";

import { queryClient } from "@/providers/query/QueryClient";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { NotificationTypes } from "./NotificationTypes";

const mockGet = vi.fn();
const mockPut = vi.fn();
vi.mock("@/api/client", () => ({
    api: {
        webui: {
            get: (...args: unknown[]) => mockGet(...args),
            put: (...args: unknown[]) => mockPut(...args),
        },
    },
}));

const categories = [
    { category: "scheduled_task", label: "Scheduled task results", group: "Agents and Tasks", description: "" },
    { category: "agent_message", label: "Messages from agents and workflows", group: "Agents and Tasks", description: "Sent when an agent or workflow finishes or finds something to report, without an open chat." },
    {
        category: "hil",
        label: "Tool approvals and agent input requests",
        group: "Requests and Sharing",
        description: "If a request expires, the agent continues without an answer, a tool call that waits for approval does not run, and a workflow step that waits for approval fails.",
    },
    { category: "chat_share", label: "Chats shared with me", group: "Requests and Sharing", description: "" },
];
const web = { channelType: "web", destinationKey: "", destination: "" };
const slack = { channelType: "slack", destinationKey: "", destination: "" };
const teams = { channelType: "teams", destinationKey: "", destination: "" };

function cellsFor(channelType: string, off: string[] = []) {
    return categories.map(c => ({
        category: c.category,
        channelType,
        destinationKey: "",
        enabled: !off.includes(c.category),
        locked: channelType === "web" && c.category === "hil",
    }));
}

const inAppOnly = { categories, channels: [web], cells: cellsFor("web") };
const withSlackAndTeams = { categories, channels: [web, slack, teams], cells: [...cellsFor("web"), ...cellsFor("slack", ["chat_share"]), ...cellsFor("teams")] };

function apiError(message: string, status: number) {
    return Object.assign(new Error(message), { status, errorId: "err-1" });
}

function proxyError(message: string, status: number) {
    return Object.assign(new Error(message), { status });
}

const addNotification = vi.fn();
const opts = { featureFlags: { notify_service: true }, configContextValues: {} };
const withNotice = { ...opts, chatContextValues: { addNotification } };

beforeEach(() => {
    mockGet.mockReset();
    mockPut.mockReset();
    addNotification.mockReset();
    mockGet.mockResolvedValue(inAppOnly);
    mockPut.mockResolvedValue(inAppOnly);
    queryClient.removeQueries({ queryKey: notificationKeys.matrix() });
    queryClient.setQueryDefaults(notificationKeys.matrix(), { retryDelay: 0 });
});

afterEach(() => vi.clearAllMocks());

describe("NotificationTypes", () => {
    it("lists the types under their group headings with their descriptions", async () => {
        renderWithProviders(<NotificationTypes />, opts);

        const tasks = await screen.findByRole("region", { name: "Agents and Tasks" });
        expect(within(tasks).getByRole("heading", { name: "Agents and Tasks" })).toBeInTheDocument();
        expect(within(tasks).getByText("Scheduled task results")).toBeInTheDocument();
        expect(within(tasks).getByText("Sent when an agent or workflow finishes or finds something to report, without an open chat.")).toBeInTheDocument();
        const requests = screen.getByRole("region", { name: "Requests and Sharing" });
        expect(within(requests).getByRole("heading", { name: "Requests and Sharing" })).toBeInTheDocument();
        expect(within(requests).getByText("Chats shared with me")).toBeInTheDocument();
        expect(within(requests).getByText("If a request expires, the agent continues without an answer, a tool call that waits for approval does not run, and a workflow step that waits for approval fails.")).toBeInTheDocument();
    });

    it("closes every section with a divider under its last item", async () => {
        renderWithProviders(<NotificationTypes />, opts);

        await screen.findByRole("region", { name: "Agents and Tasks" });
        const sections = screen.getAllByRole("region");
        expect(sections.length).toBeGreaterThan(1);
        for (const section of sections) {
            const lastRow = section.lastElementChild;
            expect(lastRow).toHaveClass("border-b");
            expect(lastRow?.className).not.toMatch(/last:border-b-0/);
        }
    });

    it("has no channel switcher while the in-app inbox is the only channel", async () => {
        renderWithProviders(<NotificationTypes />, opts);

        await screen.findByText("Scheduled task results");
        expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
        expect(screen.getByRole("switch", { name: "Scheduled task results: In-App" })).toBeChecked();
    });

    it("keeps the in-app switch for approvals locked on", async () => {
        renderWithProviders(<NotificationTypes />, opts);

        const locked = await screen.findByRole("switch", { name: "Tool approvals and agent input requests: In-App" });

        expect(locked).toBeChecked();
        expect(locked).toBeDisabled();
        expect(locked.closest("span[title]")).toHaveAttribute("title", "Tool approvals and agent input requests always reach your in-app inbox.");
    });

    it("has no Budget section while the service reports no budget types", async () => {
        renderWithProviders(<NotificationTypes />, opts);

        await screen.findByRole("region", { name: "Agents and Tasks" });
        expect(screen.queryByRole("region", { name: "Budget" })).not.toBeInTheDocument();
    });

    it("lists the budget types the service reports under Budget", async () => {
        const budgetCategories = [
            { category: "budget_self_warn", label: "Approaching my budget threshold", group: "Budget", description: "" },
            { category: "budget_self_limit", label: "My budget reached", group: "Budget", description: "" },
        ];
        mockGet.mockResolvedValue({
            categories: [...categories, ...budgetCategories],
            channels: [web],
            cells: [...cellsFor("web"), ...budgetCategories.map(c => ({ category: c.category, channelType: "web", destinationKey: "", enabled: true, locked: false }))],
        });
        renderWithProviders(<NotificationTypes />, opts);

        const budget = await screen.findByRole("region", { name: "Budget" });

        expect(within(budget).getByRole("switch", { name: "Approaching my budget threshold: In-App" })).toBeEnabled();
        expect(within(budget).getByRole("switch", { name: "My budget reached: In-App" })).toBeEnabled();
        expect(within(budget).queryByText("Not available yet.")).not.toBeInTheDocument();
    });

    it("shows a switcher with how many types are on for each channel once an application is enabled", async () => {
        mockGet.mockResolvedValue(withSlackAndTeams);
        renderWithProviders(<NotificationTypes />, opts);

        expect(await screen.findByRole("tab", { name: "In-App, 4 of 4 on" })).toBeInTheDocument();
        expect(screen.getByRole("tab", { name: "Slack, 3 of 4 on" })).toBeInTheDocument();
        expect(screen.getByRole("tab", { name: "Teams, 4 of 4 on" })).toBeInTheDocument();
        expect(screen.getByRole("tab", { name: "In-App, 4 of 4 on" })).toHaveAttribute("aria-selected", "true");
    });

    it("names Google Chat in the switcher and its switches", async () => {
        const googleChat = { channelType: "google_chat", destinationKey: "", destination: "" };
        mockGet.mockResolvedValue({ categories, channels: [web, googleChat], cells: [...cellsFor("web"), ...cellsFor("google_chat", ["chat_share"])] });
        renderWithProviders(<NotificationTypes />, opts);

        fireEvent.mouseDown(await screen.findByRole("tab", { name: "Google Chat, 3 of 4 on" }));

        expect(await screen.findByRole("switch", { name: "Chats shared with me: Google Chat" })).not.toBeChecked();
        expect(screen.getByRole("switch", { name: "Scheduled task results: Google Chat" })).toBeChecked();
    });

    it("shows the picked channel's switches", async () => {
        mockGet.mockResolvedValue(withSlackAndTeams);
        renderWithProviders(<NotificationTypes />, opts);

        fireEvent.mouseDown(await screen.findByRole("tab", { name: "Slack, 3 of 4 on" }));

        expect(await screen.findByRole("switch", { name: "Chats shared with me: Slack" })).not.toBeChecked();
        expect(screen.getByRole("switch", { name: "Scheduled task results: Slack" })).toBeChecked();
        expect(screen.queryByRole("switch", { name: "Scheduled task results: In-App" })).not.toBeInTheDocument();
    });

    it("saves a switch as soon as it is flipped and shows the new state and count at once", async () => {
        mockGet.mockResolvedValue(withSlackAndTeams);
        let finish: (value: unknown) => void = () => undefined;
        mockPut.mockReturnValue(new Promise(resolve => (finish = resolve)));
        renderWithProviders(<NotificationTypes />, opts);
        fireEvent.mouseDown(await screen.findByRole("tab", { name: "Slack, 3 of 4 on" }));

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: Slack" }));

        await waitFor(() => expect(mockPut).toHaveBeenCalledWith("/api/v1/notify/preferences/matrix", { cells: [{ category: "scheduled_task", channelType: "slack", destinationKey: "", enabled: false }] }, { signal: expect.any(AbortSignal) }));
        expect(screen.getByRole("switch", { name: "Scheduled task results: Slack" })).not.toBeChecked();
        expect(screen.getByRole("tab", { name: "Slack, 2 of 4 on" })).toBeInTheDocument();
        finish({ ...withSlackAndTeams, cells: [...cellsFor("web"), ...cellsFor("slack", ["chat_share", "scheduled_task"]), ...cellsFor("teams")] });
    });

    it("undoes a switch whose save failed and says so", async () => {
        mockGet.mockResolvedValueOnce(withSlackAndTeams).mockRejectedValue(apiError("down", 400));
        mockPut.mockRejectedValue(apiError("", 500));
        renderWithProviders(<NotificationTypes />, withNotice);

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Couldn't save your notification settings. Try again.", "warning"));
        expect(await screen.findByText("Couldn't refresh your notification settings.")).toBeInTheDocument();
        expect(screen.getByRole("alert")).toHaveTextContent("Couldn't save your notification settings. Try again.");
        await waitFor(() => expect(screen.getByRole("switch", { name: "Scheduled task results: In-App" })).toBeChecked());
    });

    it("shows why a save failed next to the switches, and clears it when the next change starts", async () => {
        mockPut.mockRejectedValueOnce(apiError("", 500)).mockResolvedValue(inAppOnly);
        renderWithProviders(<NotificationTypes />, withNotice);

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't save your notification settings. Try again.");

        fireEvent.click(screen.getByRole("switch", { name: "Messages from agents and workflows: In-App" }));
        await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    });

    it("keeps the message while another save is still in flight", async () => {
        let failFirst: (error: Error) => void = () => undefined;
        mockPut.mockReturnValueOnce(new Promise((_resolve, reject) => (failFirst = reject))).mockReturnValue(new Promise(() => undefined));
        renderWithProviders(<NotificationTypes />, withNotice);
        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));
        fireEvent.click(screen.getByRole("switch", { name: "Messages from agents and workflows: In-App" }));
        await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(1));
        failFirst(apiError("", 500));
        await screen.findByRole("alert");
        await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(2));

        fireEvent.click(screen.getByRole("switch", { name: "Chats shared with me: In-App" }));

        expect(screen.getByRole("alert")).toBeInTheDocument();
    });

    it("shows the service's words when it explains why a save failed", async () => {
        mockPut.mockRejectedValue(apiError("you have no slack channel", 400));
        renderWithProviders(<NotificationTypes />, withNotice);

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("you have no slack channel", "warning"));
        expect(screen.getByRole("alert")).toHaveTextContent("you have no slack channel");
    });

    it("does not show a proxy's text as the reason a save failed", async () => {
        mockPut.mockRejectedValue(proxyError("no healthy upstream", 503));
        renderWithProviders(<NotificationTypes />, withNotice);

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Couldn't save your notification settings. Try again.", "warning"));
        expect(screen.getByRole("alert")).toHaveTextContent("Couldn't save your notification settings. Try again.");
    });

    it.each([
        ["a router's not found", proxyError("404 page not found\n", 404)],
        ["a whitespace-only error body", proxyError("\n", 502)],
        ["the client's own sign-in text", proxyError("Not authenticated", 401)],
    ])("does not show %s as the reason a save failed", async (_name, failure) => {
        mockPut.mockRejectedValue(failure);
        renderWithProviders(<NotificationTypes />, withNotice);

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Couldn't save your notification settings. Try again.", "warning"));
        expect(screen.getByRole("alert")).toHaveTextContent("Couldn't save your notification settings. Try again.");
    });

    it.each([
        ["a save that took too long", new DOMException("signal timed out", "TimeoutError"), "Saving took too long, so your change may not have saved. Check the switches and try again."],
        ["a save that could not reach the service", new TypeError("Failed to fetch"), "Couldn't reach the service. Check your connection and try again."],
    ])("explains %s without the browser's words", async (_name, failure, message) => {
        mockPut.mockRejectedValue(failure);
        renderWithProviders(<NotificationTypes />, withNotice);

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(message, "warning"));
        expect(screen.getByRole("alert")).toHaveTextContent(message);
    });

    it("says so for every save that failed", async () => {
        mockPut.mockRejectedValueOnce(apiError("first reason", 400)).mockRejectedValueOnce(apiError("second reason", 400));
        renderWithProviders(<NotificationTypes />, withNotice);

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));
        fireEvent.click(screen.getByRole("switch", { name: "Messages from agents and workflows: In-App" }));

        await waitFor(() => expect(addNotification.mock.calls.map(call => call[0])).toEqual(["first reason", "second reason"]));
        expect(screen.getByRole("alert")).toHaveTextContent("second reason");
    });

    it("says a save failed even when the settings were closed before it did", async () => {
        let fail: (error: Error) => void = () => undefined;
        mockPut.mockReturnValue(new Promise((_resolve, reject) => (fail = reject)));
        const { unmount } = renderWithProviders(<NotificationTypes />, withNotice);
        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));
        await waitFor(() => expect(mockPut).toHaveBeenCalled());

        unmount();
        fail(apiError("", 500));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Couldn't save your notification settings. Try again.", "warning"));
    });

    it("says a save failed once when a later save succeeds, and shows what the service holds", async () => {
        const agentOff = { ...inAppOnly, cells: cellsFor("web", ["agent_message"]) };
        mockGet.mockResolvedValueOnce(inAppOnly).mockResolvedValue(agentOff);
        let failFirst: (error: Error) => void = () => undefined;
        mockPut.mockReturnValueOnce(new Promise((_resolve, reject) => (failFirst = reject))).mockResolvedValueOnce(agentOff);
        renderWithProviders(<NotificationTypes />, withNotice);

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));
        fireEvent.click(screen.getByRole("switch", { name: "Messages from agents and workflows: In-App" }));
        await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(1));
        failFirst(apiError("", 500));

        await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(addNotification).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(screen.getByRole("switch", { name: "Scheduled task results: In-App" })).toBeChecked());
        expect(screen.getByRole("switch", { name: "Messages from agents and workflows: In-App" })).not.toBeChecked();
    });

    it("keeps the switches and says so when the settings cannot be refreshed", async () => {
        renderWithProviders(<NotificationTypes />, opts);
        await screen.findByText("Scheduled task results");
        mockGet.mockRejectedValue(apiError("down", 500));

        await act(async () => {
            await queryClient.invalidateQueries({ queryKey: notificationKeys.matrix() });
        });

        expect(await screen.findByText("Couldn't refresh your notification settings.")).toBeInTheDocument();
        expect(screen.getByRole("switch", { name: "Scheduled task results: In-App" })).toBeInTheDocument();
    });

    it("falls back to the in-app channel when the picked channel is no longer there", async () => {
        mockGet.mockResolvedValue(withSlackAndTeams);
        renderWithProviders(<NotificationTypes />, opts);
        fireEvent.mouseDown(await screen.findByRole("tab", { name: "Slack, 3 of 4 on" }));
        await screen.findByRole("switch", { name: "Scheduled task results: Slack" });

        act(() => {
            queryClient.setQueryData(notificationKeys.matrix(), { categories, channels: [web, teams], cells: [...cellsFor("web"), ...cellsFor("teams")] });
        });

        expect(await screen.findByRole("switch", { name: "Scheduled task results: In-App" })).toBeChecked();
        expect(screen.queryByRole("switch", { name: "Scheduled task results: Slack" })).not.toBeInTheDocument();
        expect(screen.getByRole("tab", { name: "In-App, 4 of 4 on" })).toHaveAttribute("aria-selected", "true");
    });

    it("tells a directory destination apart by its label", async () => {
        const hook = { channelType: "webhook", destinationKey: "wh_1", destination: "hooks.example.com" };
        mockGet.mockResolvedValue({ categories, channels: [web, hook], cells: [...cellsFor("web"), ...cellsFor("webhook").map(c => ({ ...c, destinationKey: "wh_1" }))] });
        renderWithProviders(<NotificationTypes />, opts);

        expect(await screen.findByRole("tab", { name: "Webhook (hooks.example.com), 4 of 4 on" })).toBeInTheDocument();
    });

    it("saves the destination that was toggled when two webhooks share a channel type", async () => {
        const hookA = { channelType: "webhook", destinationKey: "wh_1", destination: "a.example.com" };
        const hookB = { channelType: "webhook", destinationKey: "wh_2", destination: "b.example.com" };
        const hookCells = (key: string) => cellsFor("webhook").map(c => ({ ...c, destinationKey: key }));
        const before = { categories, channels: [web, hookA, hookB], cells: [...cellsFor("web"), ...hookCells("wh_1"), ...hookCells("wh_2")] };
        const after = { ...before, cells: before.cells.map(c => (c.destinationKey === "wh_2" && c.category === "scheduled_task" ? { ...c, enabled: false } : c)) };
        mockGet.mockResolvedValue(before);
        mockPut.mockImplementation(async () => {
            mockGet.mockResolvedValue(after);
            return after;
        });
        renderWithProviders(<NotificationTypes />, opts);

        expect(await screen.findByRole("tab", { name: "Webhook (a.example.com), 4 of 4 on" })).toBeInTheDocument();
        fireEvent.mouseDown(screen.getByRole("tab", { name: "Webhook (b.example.com), 4 of 4 on" }));
        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: Webhook (b.example.com)" }));

        await waitFor(() => expect(mockPut).toHaveBeenCalledWith("/api/v1/notify/preferences/matrix", { cells: [{ category: "scheduled_task", channelType: "webhook", destinationKey: "wh_2", enabled: false }] }, { signal: expect.any(AbortSignal) }));
        expect(screen.getByRole("tab", { name: "Webhook (b.example.com), 3 of 4 on" })).toBeInTheDocument();
        expect(screen.getByRole("tab", { name: "Webhook (a.example.com), 4 of 4 on" })).toBeInTheDocument();
    });

    it("lists the types of a service that sends no group without failing", async () => {
        const { group: _group, ...ungrouped } = categories[0];
        mockGet.mockResolvedValue({ ...inAppOnly, categories: [ungrouped, { ...categories[1], group: undefined }] });
        renderWithProviders(<NotificationTypes />, opts);

        expect(await screen.findByRole("switch", { name: "Scheduled task results: In-App" })).toBeInTheDocument();
        expect(screen.getByRole("switch", { name: "Messages from agents and workflows: In-App" })).toBeInTheDocument();
    });

    it("explains a load failure and retries", async () => {
        mockGet.mockRejectedValueOnce(apiError("settings are unavailable", 503)).mockResolvedValue(inAppOnly);
        renderWithProviders(<NotificationTypes />, opts);

        expect(await screen.findByText("settings are unavailable")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Retry" }));

        expect(await screen.findByText("Scheduled task results")).toBeInTheDocument();
    });
});
