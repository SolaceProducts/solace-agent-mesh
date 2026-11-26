// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, within } from "storybook/test";

import { ShareChatDialog } from "@/features/share/ShareChatDialog";

import { withDefaults } from "@/testing/helpers";

const mockShareLink = {
    id: "share-123",
    shareUrl: "https://example.com/share/abc123xyz",
    sessionId: "test-session-123",
    createdAt: new Date(Date.now() - 86400000).toISOString(),
    snapshotTime: new Date(Date.now() - 86400000).toISOString(),
    requireAuthentication: true,
};

const mockOwner = { ownerEmail: "owner@example.com" };

const sessionDetailHandler = (updatedAt: string, ownerEmail: string = mockOwner.ownerEmail) => http.get("*/api/v1/sessions/:sessionId", () => HttpResponse.json({ updatedAt, ownerEmail }));

const mockViewerRecent = {
    userEmail: "viewer@example.com",
    addedAt: new Date(Date.now() - 3600000).toISOString(),
    accessLevel: "resource_viewer" as const,
};

const mockViewerOld = {
    userEmail: "viewer@example.com",
    addedAt: new Date(Date.now() - 172800000).toISOString(),
    accessLevel: "resource_viewer" as const,
};

const sharedArgs = {
    sessionId: "test-session-123",
    sessionTitle: "My Chat Session",
    sessionUpdatedTime: new Date().toISOString(),
    open: true,
    onOpenChange: () => {},
    onError: (error: { title: string; message: string }) => console.error(error.title, error.message),
    onSuccess: (message: string) => console.log(message),
};

const meta: Meta<typeof ShareChatDialog> = {
    title: "Chat/ShareChatDialog",
    component: ShareChatDialog,
    tags: ["autodocs"],
    parameters: {
        configContext: { identityServiceType: "local" },
        msw: {
            handlers: withDefaults(
                sessionDetailHandler(new Date(Date.now() - 604800000).toISOString()),
                http.get("*/api/v1/share/link/:sessionId", () => HttpResponse.json(mockShareLink)),
                http.get("*/api/v1/share/:shareId/users", () => HttpResponse.json({ ...mockOwner, users: [mockViewerRecent] })),
                http.post("*/api/v1/share/:shareId/users", () => HttpResponse.json({ success: true })),
                http.delete("*/api/v1/share/:shareId/users", () => HttpResponse.json({ success: true }))
            ),
        },
    },
};

export default meta;
type Story = StoryObj<typeof ShareChatDialog>;

export const Default: Story = {
    args: { ...sharedArgs },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByText("viewer@example.com");
    },
};

export const WithAddRow: Story = {
    args: { ...sharedArgs, defaultShowAddRow: true },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByPlaceholderText("Search by email...");
    },
};

export const WithPublicLink: Story = {
    args: { ...sharedArgs, defaultShowPublicLink: true },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByText("Sharing Link");
        await within(dialog).findByText(/Anyone in your organization with this link/);
    },
};

export const WithOutdatedSession: Story = {
    args: {
        ...sharedArgs,
        sessionId: "test-session-outdated",
        sessionUpdatedTime: new Date(Date.now() - 86400000).toISOString(),
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                sessionDetailHandler(new Date(Date.now() - 86400000).toISOString()),
                http.get("*/api/v1/share/link/:sessionId", () => HttpResponse.json({ ...mockShareLink, id: "share-outdated", sessionId: "test-session-outdated" })),
                http.get("*/api/v1/share/:shareId/users", () => HttpResponse.json({ ...mockOwner, users: [mockViewerOld] }))
            ),
        },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByRole("button", { name: "Update snapshot" });
    },
};

export const OnlyOwner: Story = {
    args: {
        ...sharedArgs,
        sessionId: "test-session-only-owner",
        sessionTitle: "Chat Not Shared Yet",
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                sessionDetailHandler(new Date().toISOString()),
                http.get("*/api/v1/share/link/:sessionId", () => HttpResponse.json({ ...mockShareLink, id: "share-only-owner", sessionId: "test-session-only-owner" })),
                http.get("*/api/v1/share/:shareId/users", () => HttpResponse.json({ ...mockOwner, users: [] }))
            ),
        },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByText("owner@example.com");
        expect(within(dialog).queryByText("viewer@example.com")).toBeNull();
    },
};
