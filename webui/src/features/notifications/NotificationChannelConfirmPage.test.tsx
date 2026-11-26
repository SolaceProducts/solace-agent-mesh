// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { NotificationChannelConfirmPage } from "./NotificationChannelConfirmPage";

const mockPost = vi.fn();
vi.mock("@/api/client", () => ({
    api: {
        webui: {
            post: (...args: unknown[]) => mockPost(...args),
        },
    },
}));

const NEW_CODE_HINT = "To turn the channel on, open Settings → Notifications in SAM.";
const verifyPath = "/api/v1/notify/channels/slack/confirm/verify";
const confirmUrl = "/notifications/confirm?channel=slack&code=ABCD-EFGH";

function apiError(message: string, status: number) {
    return Object.assign(new Error(message), { status });
}

beforeEach(() => {
    mockPost.mockReset();
    mockPost.mockResolvedValue({ channelType: "slack", confirmed: true, pending: false, locked: false });
});

afterEach(() => vi.clearAllMocks());

describe("NotificationChannelConfirmPage", () => {
    it("submits the code from the query once and reports success", async () => {
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: confirmUrl });

        expect(await screen.findByText(/slack confirmed/i)).toBeInTheDocument();
        expect(mockPost).toHaveBeenCalledWith(verifyPath, { code: "ABCD-EFGH" });
        expect(mockPost).toHaveBeenCalledTimes(1);
    });

    it("shows the server's reason when the code is rejected (400)", async () => {
        mockPost.mockRejectedValue(apiError("too many incorrect attempts; turn the channel on instead", 400));
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: confirmUrl });

        expect(await screen.findByText("too many incorrect attempts; turn the channel on instead")).toBeInTheDocument();
        expect(screen.getByText(NEW_CODE_HINT)).toBeInTheDocument();
        expect(screen.queryByText(/invalid or has expired/i)).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
    });

    it("does not offer Retry when the user lacks permission (403)", async () => {
        mockPost.mockRejectedValue(apiError("forbidden", 403));
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: confirmUrl });

        expect(await screen.findByText("Can't confirm this channel")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
        expect(screen.queryByText(NEW_CODE_HINT)).not.toBeInTheDocument();
    });

    it.each([503, 404])("offers Retry for a %i, which a starting or redeploying gateway also returns", async status => {
        mockPost.mockRejectedValue(apiError("gateway not ready", status));
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: confirmUrl });

        expect(await screen.findByText(/couldn't confirm right now/i)).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
        expect(screen.queryByText(NEW_CODE_HINT)).not.toBeInTheDocument();
    });

    it("falls back to the default text when a rejected code has no server message (400)", async () => {
        mockPost.mockRejectedValue(apiError("", 400));
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: confirmUrl });

        expect(await screen.findByText("This code is invalid or has expired. Open Settings → Notifications in SAM and turn the channel on.")).toBeInTheDocument();
        expect(screen.queryByText(NEW_CODE_HINT)).not.toBeInTheDocument();
    });

    it("asks the user to sign in (401)", async () => {
        mockPost.mockRejectedValue(apiError("unauthorized", 401));
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: confirmUrl });

        expect(await screen.findByText("Sign in to SAM, then open the link again.")).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "Back to SAM" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
    });

    it("offers Retry on other errors and calls verify again", async () => {
        mockPost.mockRejectedValueOnce(apiError("server error", 500));
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: confirmUrl });

        expect(await screen.findByText(/couldn't confirm right now/i)).toBeInTheDocument();
        expect(mockPost).toHaveBeenCalledTimes(1);

        fireEvent.click(screen.getByRole("button", { name: "Retry" }));

        expect(await screen.findByText(/slack confirmed/i)).toBeInTheDocument();
        await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(2));
        expect(mockPost).toHaveBeenLastCalledWith(verifyPath, { code: "ABCD-EFGH" });
    });

    it("treats a network failure (no status) as a retryable error", async () => {
        mockPost.mockRejectedValueOnce(new Error("Failed to fetch"));
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: confirmUrl });

        expect(await screen.findByText(/couldn't confirm right now/i)).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    });

    it("reports an invalid link and does not call the API when params are missing", async () => {
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: "/notifications/confirm" });

        expect(await screen.findByText(/invalid confirmation link/i)).toBeInTheDocument();
        expect(mockPost).not.toHaveBeenCalled();
    });

    it("does not call the API when the code is missing", async () => {
        renderWithProviders(<NotificationChannelConfirmPage />, { initialPath: "/notifications/confirm?channel=slack" });

        expect(await screen.findByText(/invalid confirmation link/i)).toBeInTheDocument();
        expect(mockPost).not.toHaveBeenCalled();
    });
});
