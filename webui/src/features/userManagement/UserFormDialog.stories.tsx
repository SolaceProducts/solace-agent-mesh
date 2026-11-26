// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, waitFor, within } from "storybook/test";

import { seedRbacForStory } from "@/testing/data/rbac";
import { withDefaults } from "@/testing/helpers";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";
import type { RBACRole, User } from "@/types";

import { UserFormDialog } from "./UserFormDialog";

const T = "2026-01-15T09:00:00.000+00:00";

const MAX_ROLE_NAME_LENGTH = 64;

const manyRoles: RBACRole[] = Array.from({ length: 12 }, (_, i) => {
    const n = String(i + 1).padStart(2, "0");
    const isLong = (i + 1) % 3 === 0;
    const name = isLong ? `Team Role ${n} maximum-length name for chip wrap test`.padEnd(MAX_ROLE_NAME_LENGTH, "x").slice(0, MAX_ROLE_NAME_LENGTH) : `Team Role ${n}`;
    return {
        id: `role-team-${n}`,
        name,
        description: `Custom role ${n}.`,
        origin: "custom",
        scopes: ["agent:*:invoke"],
        createdBy: "admin@example.com",
        updatedBy: "admin@example.com",
        createdAt: T,
        updatedAt: T,
    };
});

const manyRoleUser: User = {
    id: "1a000000-0000-4000-8000-0000000000ff",
    displayName: "Polly Multirole",
    email: "polly@example.com",
    sub: "oidc|polly",
    roles: manyRoles.map(r => ({ roleId: r.id, source: "direct" as const })),
};

function DialogWrapper() {
    const [open, setOpen] = useState(true);
    return <UserFormDialog user={manyRoleUser} open={open} onOpenChange={setOpen} />;
}

const meta = {
    title: "User Management/UserFormDialog",
    component: UserFormDialog,
    args: { user: manyRoleUser, open: true, onOpenChange: () => {} },
    parameters: {
        msw: { handlers: withDefaults(...rbacHandlers) },
    },
    beforeEach: () => seedRbacForStory({ roles: manyRoles, users: [manyRoleUser] }),
} satisfies Meta<typeof UserFormDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ManyRoles: Story = {
    render: () => <DialogWrapper />,
    play: async () => {
        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText("Edit User Roles");
        await waitFor(() => expect(dialog.getAllByRole("button", { name: /^Remove / })).toHaveLength(manyRoles.length));
    },
};

const noRolesUser: User = { id: "1a000000-0000-4000-8000-0000000000fe", displayName: "Roland Roleless", email: "roland@example.com", sub: "oidc|roland", roles: [] };

export const NoAssignableRoles: Story = {
    render: () => {
        function Wrapper() {
            const [open, setOpen] = useState(true);
            return <UserFormDialog user={noRolesUser} open={open} onOpenChange={setOpen} />;
        }
        return <Wrapper />;
    },
    beforeEach: () => seedRbacForStory({ roles: [], users: [noRolesUser] }),
    play: async () => {
        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText(/no assignable roles exist yet/i);
        expect(dialog.getByRole("button", { name: "Go to Roles" })).toBeInTheDocument();

        const rolesSelect = dialog.getByText("Select options...").closest('[aria-haspopup="listbox"]');
        expect(rolesSelect).toHaveAttribute("aria-disabled", "true");
    },
};
