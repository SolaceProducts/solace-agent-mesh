// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { FULL_RBAC_SEED, SEED_ROLES, seedRbacForStory } from "@/testing/data/rbac";
import { withDefaults } from "@/testing/helpers";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";

import { RoleDefaultDialog } from "./RoleDefaultDialog";

function Harness() {
    const [open, setOpen] = useState(true);
    return <RoleDefaultDialog open={open} onOpenChange={setOpen} />;
}

const meta = {
    title: "User Management/RoleDefaultDialog",
    component: RoleDefaultDialog,
    args: { open: true, onOpenChange: () => {} },
    parameters: { msw: { handlers: withDefaults(...rbacHandlers) } },
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
} satisfies Meta<typeof RoleDefaultDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    render: () => <Harness />,
    play: async () => {
        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText("Manage Default Roles");
        const checkbox = dialog.getByRole("checkbox");
        await waitFor(() => expect(checkbox).toBeChecked());
        expect(dialog.getByText("Evaluations Analyst")).toBeInTheDocument();
    },
};

export const ClearAndSave: Story = {
    render: () => <Harness />,
    play: async () => {
        const dialog = within(await screen.findByRole("dialog"));
        const checkbox = dialog.getByRole("checkbox");
        await waitFor(() => expect(checkbox).toBeChecked());

        await userEvent.click(checkbox);
        const save = dialog.getByTestId("dialogConfirmButton");
        await waitFor(() => expect(save).toBeEnabled());
        await userEvent.click(save);

        await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    },
};

export const NoRoles: Story = {
    render: () => <Harness />,
    beforeEach: () => seedRbacForStory({}),
    play: async () => {
        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText(/no assignable roles exist yet/i);
        expect(dialog.getByRole("checkbox")).toBeDisabled();
        expect(dialog.getByRole("button", { name: "Create Role" })).toBeInTheDocument();
    },
};

export const OnlyBuiltInRoles: Story = {
    render: () => <Harness />,
    beforeEach: () => seedRbacForStory({ roles: SEED_ROLES.filter(r => r.origin === "yaml") }),
    play: async () => {
        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText(/no assignable roles exist yet/i);
        expect(dialog.getByRole("checkbox")).toBeDisabled();
        expect(dialog.getByRole("button", { name: "Create Role" })).toBeInTheDocument();
    },
};
