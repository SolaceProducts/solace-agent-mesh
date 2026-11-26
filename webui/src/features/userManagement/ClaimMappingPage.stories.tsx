// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, within } from "storybook/test";

import { FULL_RBAC_SEED, MANY_CLAIM_MAPPINGS_SEED, SEED_ROLES, seedRbacForStory } from "@/testing/data/rbac";
import { withDefaults } from "@/testing/helpers";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";

import { ClaimMappingPage } from "./ClaimMappingPage";

const meta = {
    title: "User Management/Claims",
    component: ClaimMappingPage,
    parameters: {
        msw: { handlers: withDefaults(...rbacHandlers) },
        docs: {
            description: {
                component: "Claim mappings (Name | Claim Value | Type | Roles) exercised under different data configurations seeded into the mock RBAC store.",
            },
        },
    },
} satisfies Meta<typeof ClaimMappingPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {
    beforeEach: () => seedRbacForStory({}),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("No Claim Mappings Found");
        expect(canvas.getAllByRole("button", { name: "Create Claim Mapping" }).length).toBeGreaterThan(1);
    },
};

export const EmptyWithFilter: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.type(canvas.getByPlaceholderText("Filter by name or claim value"), "zzz-no-match");
        await canvas.findByText("No Claim Mappings Match Your Filters");
        expect(canvas.getByRole("button", { name: "Clear Filters" })).toBeInTheDocument();
    },
};

export const Paginated: Story = {
    beforeEach: () => seedRbacForStory(MANY_CLAIM_MAPPINGS_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Mapping 001");
        expect(canvas.queryByText("Mapping 024")).not.toBeInTheDocument();

        await userEvent.click(canvas.getByLabelText("Go to next page"));

        await canvas.findByText("Mapping 024");
        expect(canvas.queryByText("Mapping 001")).not.toBeInTheDocument();
    },
};

export const FilteredByRole: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Engineering");

        await userEvent.click(canvas.getByRole("combobox", { name: "Filter by role" }));
        await userEvent.click(await screen.findByRole("option", { name: "Administrator" }));

        await canvas.findByText("Platform Admins");
        expect(canvas.queryByText("Engineering")).not.toBeInTheDocument();
    },
};

export const CreateClaimMapping: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("button", { name: "Create Claim Mapping" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByRole("heading", { name: "Create Claim Mapping" });
        expect(dialog.getByLabelText(/Name/)).toBeInTheDocument();
        expect(dialog.getByLabelText(/Claim Value/)).toBeInTheDocument();
        expect(dialog.getByText("Roles")).toBeInTheDocument();
    },
};

export const CreateClaimMappingNoRoles: Story = {
    beforeEach: () =>
        seedRbacForStory({
            roles: SEED_ROLES.filter(r => r.origin === "yaml"),
        }),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("button", { name: "Create Claim Mapping" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText(/no assignable roles exist yet/i);
        expect(dialog.getByRole("button", { name: "Go to Roles" })).toBeInTheDocument();

        const rolesSelect = dialog.getByText("Select options...").closest('[aria-haspopup="listbox"]');
        expect(rolesSelect).toHaveAttribute("aria-disabled", "true");
        expect(dialog.getByRole("button", { name: "Create Claim Mapping" })).toBeDisabled();
    },
};

async function openRowMenu(canvas: ReturnType<typeof within>, claimValue: string) {
    const row = (await canvas.findByText(claimValue)).closest("tr");
    if (!row) throw new Error(`Row for "${claimValue}" not found`);
    await userEvent.click(within(row as HTMLElement).getByRole("button", { name: "More" }));
}

const ENGINEERING_CLAIM = "1dca4db7-9ff1-4fd5-bc28-93755f43c828";

export const ViewClaimMapping: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await openRowMenu(canvas, ENGINEERING_CLAIM);
        await userEvent.click(await screen.findByRole("menuitem", { name: "View" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText("Claim Value");
        expect(dialog.getByText("Engineering")).toBeInTheDocument();
        expect(dialog.getAllByText(ENGINEERING_CLAIM).length).toBeGreaterThan(0);
        expect(dialog.getByText("Editor")).toBeInTheDocument();
        expect(dialog.getByRole("button", { name: "Close" })).toBeInTheDocument();
    },
};

export const EditClaimMapping: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await openRowMenu(canvas, ENGINEERING_CLAIM);
        await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText("Edit Claim Mapping");
        expect(dialog.getByLabelText(/Claim Value/)).toHaveValue(ENGINEERING_CLAIM);
    },
};

export const DeleteClaimMapping: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await openRowMenu(canvas, ENGINEERING_CLAIM);
        await userEvent.click(await screen.findByRole("menuitem", { name: "Delete" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText("Delete Claim Mapping");
        expect(dialog.getByText(/will be permanently deleted/)).toBeInTheDocument();
        expect(dialog.getByRole("button", { name: "Delete" })).toBeInTheDocument();
    },
};
