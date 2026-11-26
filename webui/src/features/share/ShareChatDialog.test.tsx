// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test } from "vitest";

import { queryClient } from "@/providers/query/QueryClient";

import meta from "./ShareChatDialog.stories";

const SESSION_ID = "session-restore-button";

const shareLink = {
    id: "share-restore-1",
    shareUrl: "https://example.com/share/restore-1",
    sessionId: SESSION_ID,
    createdAt: "2024-01-01T00:00:00.000Z",
    snapshotTime: "2024-01-01T00:00:00.000Z",
    requireAuthentication: false,
};

let linkExists = false;

const server = setupServer(
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({})),
    http.get("*/api/v1/sessions/:id", () => HttpResponse.json({ updatedAt: "2024-01-01T00:00:00.000Z" })),
    http.get("*/api/v1/share/link/:sessionId", async () => {
        await delay(20);
        return linkExists ? HttpResponse.json(shareLink) : new HttpResponse(null, { status: 404 });
    }),
    http.get("*/api/v1/share/:shareId/users", () => HttpResponse.json({ ownerEmail: "owner@example.com", users: [] })),
    http.post("*/api/v1/share/:sessionId", () => {
        linkExists = true;
        return HttpResponse.json(shareLink);
    }),
    http.delete("*/api/v1/share/:shareId", () => {
        linkExists = false;
        return new HttpResponse(null, { status: 204 });
    })
);

const ShareDialogStory = composeStory(
    {
        args: {
            sessionId: SESSION_ID,
            sessionTitle: "My Chat Session",
            open: true,
            onOpenChange: () => {},
            onError: () => {},
            onSuccess: () => {},
        },
    },
    meta
);

const ShareDialogWithAddRow = composeStory(
    {
        args: {
            sessionId: SESSION_ID,
            sessionTitle: "My Chat Session",
            open: true,
            defaultShowAddRow: true,
            onOpenChange: () => {},
            onError: () => {},
            onSuccess: () => {},
        },
    },
    meta
);

describe("ShareChatDialog", () => {
    beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
    afterEach(() => server.resetHandlers());
    afterAll(() => server.close());

    beforeEach(() => {
        linkExists = false;
        queryClient.clear();
    });

    test("restores the 'Copy Sharing Link' button after the link is removed", async () => {
        const user = userEvent.setup();
        render(<ShareDialogStory />);

        const copyButton = await screen.findByRole("button", { name: "Copy Sharing Link" });

        await user.click(copyButton);

        await screen.findByText("Sharing Link");

        await user.click(screen.getByRole("button", { name: "Link actions" }));
        await user.click(await screen.findByRole("menuitem", { name: /Remove Public Link/ }));

        await waitFor(() => {
            expect(screen.queryByText("Sharing Link")).not.toBeInTheDocument();
        });
        expect(await screen.findByRole("button", { name: "Copy Sharing Link" })).toBeInTheDocument();
    });

    test("renders the chip-style typeahead when defaultShowAddRow is true", async () => {
        render(<ShareDialogWithAddRow />);
        const typeahead = await screen.findByPlaceholderText("Search by email...");
        expect(typeahead).toBeInTheDocument();
    });

    test("renders the session title in the description", async () => {
        render(<ShareDialogStory />);
        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText(/My Chat Session/)).toBeInTheDocument();
    });

    test("renders the owner row from the session detail payload even before a share link exists", async () => {
        server.use(http.get("*/api/v1/sessions/:id", () => HttpResponse.json({ ownerEmail: "session-owner@example.com", updatedAt: "2024-01-01T00:00:00.000Z" })));
        render(<ShareDialogStory />);
        await screen.findByRole("dialog");
        const ownerRow = await screen.findByTestId("owner-row");
        expect(within(ownerRow).getByText("session-owner@example.com")).toBeInTheDocument();
        expect(within(ownerRow).getByText("Owner")).toBeInTheDocument();
    });

    test("renders the owner row from the session's userId before any share link exists", async () => {
        server.use(http.get("*/api/v1/sessions/:id", () => HttpResponse.json({ id: SESSION_ID, userId: "session-user@example.com", createdBy: "session-user@example.com", updatedAt: "2024-01-01T00:00:00.000Z" })));
        render(<ShareDialogStory />);
        await screen.findByRole("dialog");
        const ownerRow = await screen.findByTestId("owner-row");
        expect(within(ownerRow).getByText("session-user@example.com")).toBeInTheDocument();
        expect(within(ownerRow).getByText("Owner")).toBeInTheDocument();
    });

    test("pins the Shared On column to a fixed 200px grid track so the header aligns with the cell", async () => {
        linkExists = true;
        server.use(http.get("*/api/v1/share/:shareId/users", () => HttpResponse.json({ ownerEmail: "owner@example.com", users: [{ userEmail: "viewer@example.com", accessLevel: "resource_viewer", addedAt: "2024-01-01T00:00:00.000Z" }] })));
        render(<ShareDialogStory />);
        const shareRow = await screen.findByTestId("share-row");
        const grid = shareRow.style.gridTemplateColumns;
        expect(grid).toContain("200px");
    });

    test("shows the per-row Update Snapshot icon when the session is newer than the user's share addedAt", async () => {
        linkExists = true;
        server.use(
            http.get("*/api/v1/sessions/:id", () => HttpResponse.json({ ownerEmail: "owner@example.com", updatedAt: "2024-06-01T00:00:00.000Z" })),
            http.get("*/api/v1/share/:shareId/users", () => HttpResponse.json({ ownerEmail: "owner@example.com", users: [{ userEmail: "viewer@example.com", accessLevel: "resource_viewer", addedAt: "2024-01-01T00:00:00.000Z" }] }))
        );
        render(<ShareDialogStory />);
        const shareRow = await screen.findByTestId("share-row");
        expect(within(shareRow).getByRole("button", { name: /update snapshot/i })).toBeInTheDocument();
    });

    test("does NOT show the per-row Update Snapshot icon when the user's share is at least as new as the session", async () => {
        linkExists = true;
        server.use(
            http.get("*/api/v1/sessions/:id", () => HttpResponse.json({ ownerEmail: "owner@example.com", updatedAt: "2024-01-01T00:00:00.000Z" })),
            http.get("*/api/v1/share/:shareId/users", () => HttpResponse.json({ ownerEmail: "owner@example.com", users: [{ userEmail: "viewer@example.com", accessLevel: "resource_viewer", addedAt: "2024-06-01T00:00:00.000Z" }] }))
        );
        render(<ShareDialogStory />);
        const shareRow = await screen.findByTestId("share-row");
        expect(within(shareRow).queryByRole("button", { name: /update snapshot/i })).not.toBeInTheDocument();
    });
});
