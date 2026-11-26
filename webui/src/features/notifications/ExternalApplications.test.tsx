// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { notificationKeys } from "@/api/notifications";

import { queryClient } from "@/providers/query/QueryClient";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ExternalApplications } from "./ExternalApplications";

const mockGet = vi.fn();
const mockPost = vi.fn();
vi.mock("@/api/client", () => ({
    api: {
        webui: {
            get: (...args: unknown[]) => mockGet(...args),
            post: (...args: unknown[]) => mockPost(...args),
        },
    },
}));

const meta = { pagination: { pageNumber: 1, pageSize: 20, totalPages: 1, count: 2, nextPage: null } };
const channel = (channelType: string, confirmed: boolean) => ({ channelType, confirmed, pending: false, locked: false });

const slackOffTeamsOn = { data: [channel("slack", false), channel("teams", true)], meta };
const bothOn = { data: [channel("slack", true), channel("teams", true)], meta };
const bothOff = { data: [channel("slack", false), channel("teams", false)], meta };
const slackOnTeamsOff = { data: [channel("slack", true), channel("teams", false)], meta };

function apiError(message: string, status: number) {
    return Object.assign(new Error(message), { status, errorId: "err-1" });
}

function proxyError(message: string, status: number) {
    return Object.assign(new Error(message), { status });
}

beforeEach(() => {
    mockGet.mockReset();
    mockPost.mockReset();
    mockGet.mockResolvedValue(slackOffTeamsOn);
    mockPost.mockResolvedValue({ ...channel("slack", true), confirmedAt: "2026-09-29T00:00:00.000Z" });
    queryClient.removeQueries({ queryKey: notificationKeys.channels() });
    queryClient.setQueryDefaults(notificationKeys.channels(), { retry: false });
});

afterEach(() => vi.clearAllMocks());

const opts = { featureFlags: { notify_service: true }, configContextValues: {} };

function card(name: string): HTMLElement {
    return screen.getByText(name, { selector: "span" }).closest("div.rounded-md") as HTMLElement;
}

describe("ExternalApplications", () => {
    it("explains what enabling does and lists each application with its status", async () => {
        renderWithProviders(<ExternalApplications />, opts);

        expect(await screen.findByText("External Application Notifications")).toBeInTheDocument();
        await screen.findByRole("button", { name: "Enable Slack" });
        expect(screen.getByText("Enable an external application to receive notifications there, then choose which notification types it receives above.")).toBeInTheDocument();
        expect(within(card("Slack")).getByText("Not enabled")).toBeInTheDocument();
        expect(within(card("Teams")).getByText("Enabled")).toBeInTheDocument();
    });

    it("offers Enable for an application that is off and a menu for one that is on", async () => {
        renderWithProviders(<ExternalApplications />, opts);

        await screen.findByText("Slack");

        expect(within(card("Slack")).getByRole("button", { name: "Enable Slack" })).toBeInTheDocument();
        expect(within(card("Slack")).queryByRole("button", { name: "Slack actions" })).not.toBeInTheDocument();
        expect(within(card("Teams")).getByRole("button", { name: "Teams actions" })).toBeInTheDocument();
        expect(within(card("Teams")).queryByRole("button", { name: "Enable Teams" })).not.toBeInTheDocument();
    });

    it("lists Google Chat by name with its logo and enables it like the others", async () => {
        mockGet.mockResolvedValueOnce({ data: [channel("google_chat", false)], meta }).mockResolvedValue({ data: [channel("google_chat", true)], meta });
        renderWithProviders(<ExternalApplications />, opts);

        expect(await screen.findByText("Google Chat", { selector: "span" })).toBeInTheDocument();
        expect(card("Google Chat").querySelector("svg")).not.toBeNull();
        expect(within(card("Google Chat")).getByText("Not enabled")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Enable Google Chat" }));

        await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/channels/google_chat/confirm"));
        expect(await screen.findByText(/Google Chat is enabled\. Agent Mesh may send a short message there/)).toBeInTheDocument();
        expect(within(card("Google Chat")).getByText("Enabled")).toBeInTheDocument();
    });

    it("tells the person to message the Google Chat application first when the server gives no reason", async () => {
        mockGet.mockResolvedValue({ data: [channel("google_chat", false)], meta });
        mockPost.mockRejectedValue(apiError("", 400));
        renderWithProviders(<ExternalApplications />, opts);

        fireEvent.click(await screen.findByRole("button", { name: "Enable Google Chat" }));

        expect(await screen.findByText(/has not received a message from this user in Google Chat\. Send a message to the Agent Mesh application in Google Chat first/)).toBeInTheDocument();
    });

    it("enables an application with one click, adds it to the notification list and says what to expect", async () => {
        const invalidate = vi.spyOn(queryClient, "invalidateQueries");
        try {
            mockGet.mockResolvedValueOnce(slackOffTeamsOn).mockResolvedValue(bothOn);
            renderWithProviders(<ExternalApplications />, opts);

            fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));

            await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/channels/slack/confirm"));
            await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.matrix() }));
            expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.channels() });
            expect(await screen.findByText(/Slack is enabled\. Agent Mesh may send a short message there/)).toBeInTheDocument();
            expect(within(card("Slack")).getByText("Enabled")).toBeInTheDocument();
            expect(screen.queryByText(/Teams is enabled/)).not.toBeInTheDocument();
        } finally {
            invalidate.mockRestore();
        }
    });

    it("shows the enabled note only on the card that was enabled", async () => {
        mockGet.mockResolvedValueOnce(bothOff).mockResolvedValue(slackOnTeamsOff);
        renderWithProviders(<ExternalApplications />, opts);

        fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));

        expect(await screen.findByText(/Slack is enabled\. Agent Mesh may send a short message there/)).toBeInTheDocument();
        expect(screen.queryByText(/Teams is enabled/)).not.toBeInTheDocument();
        expect(within(card("Teams")).getByRole("button", { name: "Enable Teams" })).toBeInTheDocument();
    });

    it("shows the enabled note in an info banner that can be dismissed", async () => {
        mockGet.mockResolvedValueOnce(slackOffTeamsOn).mockResolvedValue(bothOn);
        renderWithProviders(<ExternalApplications />, opts);

        fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));

        const note = await screen.findByText("Slack is enabled. Agent Mesh may send a short message there. If notifications do not arrive, check that the Agent Mesh application can message this user.");
        const banner = note.closest("[data-testid='messageBanner']") as HTMLElement;
        expect(banner).toHaveClass("bg-(--info-w10)");
        fireEvent.click(within(banner).getByRole("button", { name: "Dismiss" }));

        await waitFor(() => expect(screen.queryByText(/Slack is enabled/)).not.toBeInTheDocument());
        expect(within(card("Slack")).getByText("Enabled")).toBeInTheDocument();
    });

    it("shows a failed enable in an error banner that can be dismissed", async () => {
        mockPost.mockRejectedValue(apiError("Unable to enable notifications because Agent Mesh has not received a message from this user in Slack. Send a message to the Agent Mesh application in Slack first.", 400));
        renderWithProviders(<ExternalApplications />, opts);

        fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));

        const banner = (await screen.findByText(/has not received a message from this user in Slack/)).closest("[data-testid='messageBanner']") as HTMLElement;
        expect(banner).toHaveClass("bg-(--error-w10)");
        fireEvent.click(within(banner).getByRole("button", { name: "Dismiss" }));

        await waitFor(() => expect(screen.queryByText(/has not received a message/)).not.toBeInTheDocument());
        expect(within(card("Slack")).getByRole("button", { name: "Enable Slack" })).toBeEnabled();
    });

    it("shows a failed disable in an error banner that can be dismissed", async () => {
        mockPost.mockRejectedValue(apiError("", 500));
        renderWithProviders(<ExternalApplications />, opts);
        await screen.findByText("Teams");

        await userEvent.setup().click(within(card("Teams")).getByRole("button", { name: "Teams actions" }));
        await userEvent.setup().click(await screen.findByRole("menuitem", { name: "Disable" }));
        const banner = (await screen.findByText("Couldn't disable Teams. Try again.")).closest("[data-testid='messageBanner']") as HTMLElement;
        fireEvent.click(within(banner).getByRole("button", { name: "Dismiss" }));

        await waitFor(() => expect(screen.queryByText("Couldn't disable Teams. Try again.")).not.toBeInTheDocument());
    });

    it("shows no enabled note when the service did not confirm the application, whatever the list says", async () => {
        mockGet.mockResolvedValueOnce(slackOffTeamsOn).mockResolvedValue(bothOn);
        mockPost.mockResolvedValue(channel("slack", false));
        renderWithProviders(<ExternalApplications />, opts);

        fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));

        await waitFor(() => expect(within(card("Slack")).getByText("Enabled")).toBeInTheDocument());
        expect(screen.queryByText(/is enabled\. Agent Mesh may send/)).not.toBeInTheDocument();
    });

    it("shows no enabled note until the list reads the application as enabled", async () => {
        mockPost.mockResolvedValue(channel("slack", true));
        renderWithProviders(<ExternalApplications />, opts);

        fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));

        await waitFor(() => expect(mockGet.mock.calls.length).toBeGreaterThan(1));
        await waitFor(() => expect(within(card("Slack")).getByRole("button", { name: "Enable Slack" })).toBeEnabled());
        expect(screen.queryByText(/is enabled\. Agent Mesh may send/)).not.toBeInTheDocument();
    });

    it("shows Enabling… on the clicked card only and sends one request while it is in flight", async () => {
        mockGet.mockResolvedValue(bothOff);
        let finish: (value: unknown) => void = () => undefined;
        mockPost.mockReturnValue(new Promise(resolve => (finish = resolve)));
        renderWithProviders(<ExternalApplications />, opts);

        fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));

        const pending = await screen.findByRole("button", { name: "Enabling… Slack" });
        expect(pending).toBeDisabled();
        fireEvent.click(pending);
        expect(mockPost).toHaveBeenCalledTimes(1);
        const other = within(card("Teams")).getByRole("button", { name: "Enable Teams" });
        expect(other).toBeDisabled();
        expect(screen.queryByRole("button", { name: "Enabling… Teams" })).not.toBeInTheDocument();
        finish(channel("slack", true));
    });

    it("disables an enabled application from its menu and refreshes the notification list", async () => {
        const invalidate = vi.spyOn(queryClient, "invalidateQueries");
        try {
            mockPost.mockResolvedValue(channel("teams", false));
            renderWithProviders(<ExternalApplications />, opts);
            await screen.findByText("Teams");

            await userEvent.setup().click(within(card("Teams")).getByRole("button", { name: "Teams actions" }));
            const disableItem = await screen.findByRole("menuitem", { name: "Disable" });
            expect(disableItem.className).not.toMatch(/error/);
            await userEvent.setup().click(disableItem);

            await waitFor(() => expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/channels/teams/disable"));
            await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.matrix() }));
            expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.channels() });
        } finally {
            invalidate.mockRestore();
        }
    });

    it.each([
        ["names the application when disabling failed", apiError("", 500), "Couldn't disable Teams. Try again."],
        ["says when disabling is not allowed", apiError("forbidden", 403), "You don't have permission to do this."],
    ])("%s", async (_name, error, message) => {
        mockPost.mockRejectedValue(error);
        renderWithProviders(<ExternalApplications />, opts);
        await screen.findByText("Teams");

        await userEvent.setup().click(within(card("Teams")).getByRole("button", { name: "Teams actions" }));
        await userEvent.setup().click(await screen.findByRole("menuitem", { name: "Disable" }));

        expect(await screen.findByRole("alert")).toHaveTextContent(message);
        await waitFor(() => expect(mockGet.mock.calls.length).toBeGreaterThan(1));
        expect(within(card("Teams")).getByText("Enabled")).toBeInTheDocument();
    });

    it("keeps the cards and says so when refreshing the list fails after a change", async () => {
        mockPost.mockResolvedValue(channel("teams", false));
        mockGet.mockResolvedValueOnce(slackOffTeamsOn).mockRejectedValue(apiError("nope", 500));
        renderWithProviders(<ExternalApplications />, opts);
        await screen.findByText("Teams");

        await userEvent.setup().click(within(card("Teams")).getByRole("button", { name: "Teams actions" }));
        await userEvent.setup().click(await screen.findByRole("menuitem", { name: "Disable" }));

        const banner = (await screen.findByText("Couldn't refresh your external applications.")).closest("[data-testid='messageBanner']") as HTMLElement;
        expect(banner).toHaveClass("bg-(--error-w10)");
        expect(within(banner).queryByRole("button", { name: "Dismiss" })).not.toBeInTheDocument();
        expect(screen.getByText("Teams")).toBeInTheDocument();
        expect(screen.queryByText("Couldn't load your external applications.")).not.toBeInTheDocument();
    });

    it.each([
        ["the server's words for a bad request", apiError("no Slack account matches your login email", 400), "no Slack account matches your login email"],
        [
            "a prompt to message the bot when the server gave no words",
            proxyError("Bad Request", 400),
            "Unable to enable notifications because Agent Mesh has not received a message from this user in Slack. Send a message to the Agent Mesh application in Slack first.",
        ],
        ["the server's words when the entry point is down", apiError("the Slack entry point is not responding", 503), "the Slack entry point is not responding"],
        ["the server's words when the entry point cannot be reached", apiError("the Slack entry point did not answer", 502), "the Slack entry point did not answer"],
        ["a permission message", apiError("forbidden", 403), "You don't have permission to do this."],
        ["a generic message for anything else", apiError("boom", 500), "Couldn't enable the application. Try again."],
        ["a generic message when a proxy answers for the entry point", proxyError("no healthy upstream", 503), "Couldn't enable the application. Try again."],
    ])("explains a failed enable with %s", async (_name, error, message) => {
        mockPost.mockRejectedValue(error);
        renderWithProviders(<ExternalApplications />, opts);

        fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));

        expect(await screen.findByText(message)).toBeInTheDocument();
        expect(within(card("Slack")).getByText("Not enabled")).toBeInTheDocument();
    });

    it("says no applications are available when the service offers none", async () => {
        mockGet.mockResolvedValue({ data: [], meta });
        renderWithProviders(<ExternalApplications />, opts);

        expect(await screen.findByText(/No external applications are available yet/)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Enable/ })).not.toBeInTheDocument();
    });

    it("explains a load failure and retries", async () => {
        mockGet.mockRejectedValueOnce(apiError("nope", 500)).mockResolvedValue(slackOffTeamsOn);
        renderWithProviders(<ExternalApplications />, opts);

        expect(await screen.findByText("Couldn't load your external applications.")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Retry" }));

        expect(await screen.findByText("Slack")).toBeInTheDocument();
    });
});
