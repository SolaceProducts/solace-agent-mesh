// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";
import { notificationKeys } from "@/api/notifications";

import { queryClient } from "@/providers/query/QueryClient";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ExternalApplications } from "./ExternalApplications";
import { NotificationTypes } from "./NotificationTypes";

const web = { channelType: "web", destinationKey: "", destination: "" };
const categories = [{ category: "scheduled_task", label: "Scheduled task results", group: "Agents and Tasks", description: "" }];
const matrix = { categories, channels: [web], cells: [{ category: "scheduled_task", channelType: "web", destinationKey: "", enabled: true, locked: false }] };
const channels = { data: [{ channelType: "slack", confirmed: false, pending: false, locked: false }], meta: { pagination: { pageNumber: 1, pageSize: 20, totalPages: 1, count: 1, nextPage: null } } };

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

function serve(failure: () => Response) {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
        const url = String(input);
        const write = (init?.method ?? "GET") !== "GET";
        if (write) return failure();
        return json(url.includes("/preferences/matrix") ? matrix : channels);
    });
}

const SAVE_FAILED = "Couldn't save your notification settings. Try again.";
const ENABLE_FAILED = "Couldn't enable the application. Try again.";
const opts = { featureFlags: { notify_service: true }, configContextValues: {} };

beforeEach(() => {
    localStorage.clear();
    api.configure("", "");
    queryClient.removeQueries({ queryKey: notificationKeys.all });
    queryClient.setQueryDefaults(notificationKeys.channels(), { retry: false });
    queryClient.setQueryDefaults(notificationKeys.matrix(), { retry: false });
});

afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
});

describe("a failed save, through the real client", () => {
    it.each([
        ["a 500 the service explained", () => json({ message: "failed to save notification settings", errorId: "e-1" }, 500), "failed to save notification settings"],
        ["a 400 the service explained", () => json({ message: "that setting is not available", errorId: "e-2" }, 400), "that setting is not available"],
        ["a 502 from a proxy with a plain-text body", () => new Response("no healthy upstream", { status: 502 }), SAVE_FAILED],
        ["a 404 from a router", () => new Response("404 page not found\n", { status: 404 }), SAVE_FAILED],
        ["a 503 html page", () => new Response("<html><body>unavailable</body></html>", { status: 503 }), SAVE_FAILED],
        ["a whitespace-only body", () => new Response("\n", { status: 502 }), SAVE_FAILED],
        ["an envelope with no message", () => json({ message: "", errorId: "e-3" }, 500), SAVE_FAILED],
    ])("shows %s as the right words", async (_name, failure, message) => {
        serve(failure);
        const addNotification = vi.fn();
        renderWithProviders(<NotificationTypes />, { ...opts, chatContextValues: { addNotification } });

        fireEvent.click(await screen.findByRole("switch", { name: "Scheduled task results: In-App" }));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(message, "warning"));
        expect(screen.getByRole("alert")).toHaveTextContent(message);
    });
});

describe("a failed enable, through the real client", () => {
    it.each([
        ["a 503 the entry point's service explained", () => json({ message: "the Slack entry point is not responding", errorId: "e-4" }, 503), "the Slack entry point is not responding"],
        ["a 502 the service explained", () => json({ message: "the Slack entry point did not answer", errorId: "e-5" }, 502), "the Slack entry point did not answer"],
        ["a 503 from a proxy with a plain-text body", () => new Response("no healthy upstream", { status: 503 }), ENABLE_FAILED],
        ["a 502 from a proxy with a plain-text body", () => new Response("Bad Gateway", { status: 502 }), ENABLE_FAILED],
    ])("shows %s as the right words", async (_name, failure, message) => {
        serve(failure);
        renderWithProviders(<ExternalApplications />, opts);

        fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));

        expect(await screen.findByText(message)).toBeInTheDocument();
    });
});
