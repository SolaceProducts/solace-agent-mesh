// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import type { CollaborativeUser } from "@/types/collaboration";

import { UserPresenceAvatars } from "@/features/chat/UserPresenceAvatars";

const mockCollaborativeUsers: Record<string, CollaborativeUser> = {
    alice: {
        id: "user-alice-001",
        name: "Olive Operations",
        email: "olive@company.com",
        role: "owner",
        isOnline: true,
    },
    bob: {
        id: "user-bob-002",
        name: "Parminder Procurement",
        email: "parminder@company.com",
        role: "collaborator",
        isOnline: true,
    },
    charlie: {
        id: "user-charlie-003",
        name: "Charlie Chang",
        email: "charlie@company.com",
        role: "collaborator",
        isOnline: false,
        lastSeen: new Date(Date.now() - 2 * 60 * 60 * 1000),
    },
};

const meta: Meta<typeof UserPresenceAvatars> = {
    title: "Chat/UserPresenceAvatars",
    component: UserPresenceAvatars,
    tags: ["autodocs"],
    parameters: {
        layout: "centered",
    },
};

export default meta;

type Story = StoryObj<typeof UserPresenceAvatars>;

export const Default: Story = {
    args: {
        users: [
            { ...mockCollaborativeUsers.alice, isOnline: true },
            { ...mockCollaborativeUsers.bob, isOnline: true },
        ],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText("OO")).toBeInTheDocument();
        await expect(canvas.getByText("PP")).toBeInTheDocument();
    },
};

export const ManyUsers: Story = {
    args: {
        users: [
            mockCollaborativeUsers.alice,
            mockCollaborativeUsers.bob,
            { ...mockCollaborativeUsers.charlie, isOnline: true },
            { ...mockCollaborativeUsers.alice, id: "user-4", name: "User Four", email: "user4@example.com", isOnline: true },
            { ...mockCollaborativeUsers.bob, id: "user-5", name: "User Five", email: "user5@example.com", isOnline: true },
            { ...mockCollaborativeUsers.charlie, id: "user-6", name: "User Six", email: "user6@example.com", isOnline: true },
            { ...mockCollaborativeUsers.alice, id: "user-7", name: "User Seven", email: "user7@example.com", isOnline: true },
        ],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText("+2")).toBeInTheDocument();
    },
};
