// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { mockApiUserBudgets } from "@/testing/data";
import { BUDGET_RBAC_SEED, FULL_RBAC_SEED, MANY_USERS_SEED, SEED_USERS, seedRbacForStory } from "@/testing/data/rbac";
import { withDefaults } from "@/testing/helpers";
import { budgetsListHandler, budgetTopUpHandlers } from "@/testing/msw/budgetHandlers";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";

import { UsersPage } from "./UsersPage";

const ROLE_GRANTS_URL = "*/api/v2/platform/users/*/roleGrants";

const meta = {
    title: "User Management/Users",
    component: UsersPage,
    parameters: {
        userContext: { userHasCapability: (capability: string) => capability !== CAPABILITIES.readBudgets },
        msw: { handlers: withDefaults(...rbacHandlers) },
        docs: {
            description: {
                component: "The users list, exercised under different data configurations seeded into the mock RBAC store.",
            },
        },
    },
} satisfies Meta<typeof UsersPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {
    beforeEach: () => seedRbacForStory({}),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("No Users Found");
    },
};

const ALICE_ID = SEED_USERS[0].id;

export const WithAdditionalAmount: Story = {
    beforeEach: () => seedRbacForStory({ ...FULL_RBAC_SEED, ...BUDGET_RBAC_SEED, topUpByUser: { [ALICE_ID]: { amount: "100.00", expiresTime: "2099-02-01T00:00:00.000Z" } } }),
    parameters: {
        userContext: { userHasCapability: () => true },
        msw: { handlers: [budgetsListHandler(mockApiUserBudgets), ...withDefaults(...budgetTopUpHandlers, ...rbacHandlers)] },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const alice = within((await canvas.findByText("Alice Anderson")).closest("tr")!);
        expect(await alice.findByText("+ $100.00")).toBeInTheDocument();

        const carolRow = () => within(canvas.getByText("Carol Clarke").closest("tr")!);
        expect(carolRow().queryByText(/^\+ /)).toBeNull();
        await userEvent.click(carolRow().getByTestId("userRowMoreTrigger"));
        await userEvent.click(await screen.findByRole("menuitem", { name: "Manage Budget" }));
        const dialog = within(await screen.findByRole("dialog"));
        const amount = dialog.getByRole("textbox", { name: "Additional Temporary Amount (USD)" });
        await waitFor(() => expect(amount).toBeEnabled());
        await userEvent.type(amount, "50");
        await userEvent.click(dialog.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(carolRow().getByText("+ $50.00")).toBeInTheDocument());
    },
};

export const WithBudgetsReadOnly: Story = {
    beforeEach: () => seedRbacForStory({ ...FULL_RBAC_SEED, ...BUDGET_RBAC_SEED, budgetByUser: {} }),
    parameters: {
        userContext: { userHasCapability: (capability: string) => capability !== CAPABILITIES.assignBudgets },
        msw: { handlers: [budgetsListHandler(mockApiUserBudgets), ...withDefaults(...rbacHandlers)] },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const alice = within((await canvas.findByText("Alice Anderson")).closest("tr")!);
        expect(await alice.findByText("Standard Monthly")).toBeInTheDocument();
        await userEvent.click(alice.getByTestId("userRowMoreTrigger"));
        expect((await screen.findAllByRole("menuitem")).map(item => item.textContent)).toEqual(["Edit User Roles"]);
    },
};

export const FilterByBudget: Story = {
    beforeEach: () => seedRbacForStory({ ...FULL_RBAC_SEED, ...BUDGET_RBAC_SEED }),
    parameters: {
        userContext: { userHasCapability: () => true },
        msw: { handlers: [budgetsListHandler(mockApiUserBudgets), ...withDefaults(...rbacHandlers)] },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(within((await canvas.findByText("Alice Anderson")).closest("tr")!).getByText("Engineering")).toBeInTheDocument();
        expect(canvas.getByRole("combobox", { name: "Filter by budget" })).toBeEnabled();
    },
};

export const Paginated: Story = {
    beforeEach: () => seedRbacForStory(MANY_USERS_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("User 001");
        expect(canvas.getByText("User 020")).toBeInTheDocument();
        expect(canvas.queryByText("User 021")).not.toBeInTheDocument();

        await userEvent.click(canvas.getByLabelText("Go to next page"));
        await canvas.findByText("User 021");
        expect(canvas.queryByText("User 001")).not.toBeInTheDocument();
    },
};

export const EmptyWithFilter: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.type(canvas.getByPlaceholderText("Filter by name or email"), "zzz-no-match");
        await canvas.findByText("No Users Match Your Filters");
        expect(canvas.getByRole("button", { name: "Clear Filters" })).toBeInTheDocument();
    },
};

export const EditUser: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const row = (await canvas.findByText("Alice Anderson")).closest("tr");
        if (!row) throw new Error("Alice's row not found");
        await userEvent.click(within(row as HTMLElement).getByRole("button", { name: "More" }));
        await userEvent.click(await screen.findByRole("menuitem", { name: "Edit User Roles" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText("Edit User Roles");
        expect(dialog.getByText(/Alice Anderson/)).toBeInTheDocument();
        expect(dialog.getByText("Roles")).toBeInTheDocument();
    },
};

export const EditUserOffersInheritedRoleInPicker: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const row = (await canvas.findByText("Dave Davies")).closest("tr");
        if (!row) throw new Error("Dave's row not found");
        await userEvent.click(within(row as HTMLElement).getByRole("button", { name: "More" }));
        await userEvent.click(await screen.findByRole("menuitem", { name: "Edit User Roles" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText("Edit User Roles");
        expect(await dialog.findByTitle("HR Agent User")).toBeInTheDocument();
        expect(dialog.queryByTitle("Evaluations Analyst")).not.toBeInTheDocument();

        const trigger = await dialog.findByRole("button", { expanded: false });
        await userEvent.click(trigger);
        await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "true"));
        expect(await screen.findByRole("button", { name: "Evaluations Analyst" })).toBeInTheDocument();
    },
};

export const EditUserSaveSuccess: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const carolRow = async () => (await canvas.findByText("Carol Clarke")).closest("tr") as HTMLElement;
        expect(within(await carolRow()).queryByText("Evaluations Analyst")).not.toBeInTheDocument();

        await userEvent.click(within(await carolRow()).getByRole("button", { name: "More" }));
        await userEvent.click(await screen.findByRole("menuitem", { name: "Edit User Roles" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText("Edit User Roles");
        await userEvent.click(await dialog.findByRole("button", { expanded: false }));
        await userEvent.click(await screen.findByRole("button", { name: "Evaluations Analyst" }));
        await userEvent.click(dialog.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
        await within(await carolRow()).findByText("Evaluations Analyst");
    },
};

export const EditUserGrantsLoadError: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(ROLE_GRANTS_URL, () => HttpResponse.json({ message: "Could not load the user's roles.", errorId: "err-load" }, { status: 500 })),
                ...rbacHandlers
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const row = (await canvas.findByText("Alice Anderson")).closest("tr");
        if (!row) throw new Error("Alice's row not found");
        await userEvent.click(within(row as HTMLElement).getByRole("button", { name: "More" }));
        await userEvent.click(await screen.findByRole("menuitem", { name: "Edit User Roles" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText("Edit User Roles");

        expect(await dialog.findByText("Could not load the user's roles.")).toBeInTheDocument();
        expect(dialog.getByRole("button", { name: "Save" })).toBeDisabled();
    },
};

const UNMATCHED_ROLE_ID = "3f2a6c1e-8b7d-5e4f-9a0b-1c2d3e4f5a6b";

export const UnknownRole: Story = {
    beforeEach: () => seedRbacForStory({ ...FULL_RBAC_SEED, users: [{ ...SEED_USERS[0], roles: [{ roleId: UNMATCHED_ROLE_ID, source: "yaml" }] }] }),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const chip = await canvas.findByText(`Unknown role ${UNMATCHED_ROLE_ID.slice(0, 8)}`);
        expect(canvas.queryByText(UNMATCHED_ROLE_ID)).not.toBeInTheDocument();
        await userEvent.hover(chip);
        expect((await screen.findAllByText(`Granted through Agent Mesh configuration. No role has the id ${UNMATCHED_ROLE_ID}`)).length).toBeGreaterThan(0);
    },
};
