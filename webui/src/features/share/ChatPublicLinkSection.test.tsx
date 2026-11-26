// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { formatDateYMD, formatTimestamp } from "@/utils/format";

import type { ShareLink } from "@/types/share";

import { ChatPublicLinkSection } from "./ChatPublicLinkSection";

const shareLink: ShareLink = {
    id: "share-1",
    sessionId: "session-1",
    title: "Test",
    isPublic: true,
    requireAuthentication: false,
    allowedDomains: [],
    accessType: "public",
    shareUrl: "https://example.com/#/share/share-1",
    createdAt: "2024-01-01T00:00:00.000Z",
    snapshotTime: "2024-06-15T00:00:00.000Z",
};

let snapshotShouldFail = false;
const snapshotCalls: string[] = [];

const server = setupServer(
    http.post("*/api/v1/share/:shareId/updateSnapshot", ({ params }) => {
        snapshotCalls.push(String(params.shareId));
        if (snapshotShouldFail) return new HttpResponse(JSON.stringify({ message: "boom" }), { status: 500 });
        return HttpResponse.json({ snapshotTime: 1700000000 });
    })
);

function renderSection(overrides: Partial<React.ComponentProps<typeof ChatPublicLinkSection>> = {}) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const props: React.ComponentProps<typeof ChatPublicLinkSection> = {
        shareLink,
        publicLinkCopied: false,
        onCopy: vi.fn(),
        onDelete: vi.fn(),
        onError: vi.fn(),
        onSuccess: vi.fn(),
        ...overrides,
    };
    render(
        <QueryClientProvider client={client}>
            <ChatPublicLinkSection {...props} />
        </QueryClientProvider>
    );
    return props;
}

describe("ChatPublicLinkSection", () => {
    beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
    afterEach(() => server.resetHandlers());
    afterAll(() => server.close());

    beforeEach(() => {
        snapshotShouldFail = false;
        snapshotCalls.length = 0;
    });

    test("displays the snapshot date-time (not the created date) under a Snapshot label", () => {
        renderSection();
        expect(screen.getByText("Snapshot")).toBeInTheDocument();
        expect(screen.getByText(formatTimestamp(shareLink.snapshotTime))).toBeInTheDocument();
        expect(screen.queryByText(formatDateYMD(shareLink.createdAt))).not.toBeInTheDocument();
    });

    test("clicking the copy button invokes onCopy", async () => {
        const user = userEvent.setup();
        const props = renderSection();
        await user.click(screen.getByRole("button", { name: /copy share link/i }));
        expect(props.onCopy).toHaveBeenCalledTimes(1);
    });

    test("clicking Refresh snapshot calls the update-snapshot API and fires onSuccess", async () => {
        const user = userEvent.setup();
        const props = renderSection();
        await user.click(screen.getByRole("button", { name: /refresh public link snapshot/i }));
        await waitFor(() => expect(snapshotCalls.length).toBeGreaterThan(0));
        expect(snapshotCalls[0]).toBe("share-1");
        await waitFor(() => expect(props.onSuccess).toHaveBeenCalledWith("Snapshot updated"));
    });

    test("Refresh snapshot fires onError when the update call fails", async () => {
        snapshotShouldFail = true;
        const user = userEvent.setup();
        const props = renderSection();
        await user.click(screen.getByRole("button", { name: /refresh public link snapshot/i }));
        await waitFor(() => expect(props.onError).toHaveBeenCalledWith(expect.objectContaining({ title: "Failed to Update Snapshot" })));
    });

    test("clicking Preview Chat opens the share URL in a new window", async () => {
        const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);
        const user = userEvent.setup();
        renderSection();
        await user.click(screen.getByRole("button", { name: /link actions/i }));
        await user.click(await screen.findByRole("menuitem", { name: /preview chat/i }));
        expect(openSpy).toHaveBeenCalledWith(shareLink.shareUrl, "_blank");
        openSpy.mockRestore();
    });
});
