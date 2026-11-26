// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import { FULL_RBAC_SEED, SEED_DEFAULT_ROLE_IDS, SEED_ROLES, seedRbacForStory } from "@/testing/data/rbac";
import { withDefaults } from "@/testing/helpers";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";

import { RolesPage } from "./RolesPage";

const ROLES_URL = "*/api/v2/platform/rbac/roles";
const DEFAULT_ROLES_URL = "*/api/v2/platform/rbac/defaultRoles";

const meta = {
    title: "User Management/Roles",
    component: RolesPage,
    parameters: {
        msw: { handlers: withDefaults(...rbacHandlers) },
        docs: {
            description: {
                component: "Roles management, exercised under different data configurations seeded into the mock RBAC store.",
            },
        },
    },
} satisfies Meta<typeof RolesPage>;

export default meta;
type Story = StoryObj<typeof meta>;

async function openRoleRowMenu(canvas: ReturnType<typeof within>, name: string) {
    const row = (await canvas.findByText(name)).closest("tr");
    if (!row) throw new Error(`Row for "${name}" not found`);
    await userEvent.click(within(row as HTMLElement).getByRole("button", { name: "More" }));
}

export const EmptyRoles: Story = {
    beforeEach: () => seedRbacForStory({}),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("No Roles Found");
        expect(canvas.getAllByRole("button", { name: "Create Role" }).length).toBeGreaterThan(1);
    },
};

export const EmptyRolesWithFilter: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.type(canvas.getByPlaceholderText("Filter by name"), "zzz-no-match");
        await canvas.findByText("No Roles Match Your Filters");
        expect(canvas.getByRole("button", { name: "Clear Filters" })).toBeInTheDocument();
    },
};

export const FilterByType: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Editor");
        expect(canvas.getByText("sam_manager")).toBeInTheDocument();
        expect(canvas.getByText("HR Agent User")).toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("roleTypeFilter"));
        await userEvent.click(await screen.findByRole("option", { name: "Custom" }));
        await canvas.findByText("HR Agent User");
        expect(canvas.queryByText("Editor")).not.toBeInTheDocument();
        expect(canvas.queryByText("sam_manager")).not.toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("roleTypeFilter"));
        await userEvent.click(await screen.findByRole("option", { name: "System" }));
        await canvas.findByText("Editor");
        expect(canvas.queryByText("sam_manager")).not.toBeInTheDocument();
        expect(canvas.queryByText("HR Agent User")).not.toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("roleTypeFilter"));
        await userEvent.click(await screen.findByRole("option", { name: "Built-in" }));
        await canvas.findByText("sam_manager");
        expect(canvas.queryByText("Editor")).not.toBeInTheDocument();
        expect(canvas.queryByText("HR Agent User")).not.toBeInTheDocument();
    },
};

export const FilterByTypeEmpty: Story = {
    beforeEach: () => seedRbacForStory({ roles: SEED_ROLES.filter(r => r.origin === "yaml") }),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Editor");
        await userEvent.click(canvas.getByTestId("roleTypeFilter"));
        await userEvent.click(await screen.findByRole("option", { name: "Custom" }));
        await canvas.findByText("No Roles Match Your Filters");
        expect(canvas.getByRole("button", { name: "Clear Filters" })).toBeInTheDocument();
    },
};

export const CreateRole: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("button", { name: "Create Role" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByRole("heading", { name: "Create Role" });
        expect(dialog.getByLabelText(/Name/)).toBeInTheDocument();
        expect(dialog.getByRole("tab", { name: "Access" })).toBeInTheDocument();
    },
};

export const ViewRole: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await openRoleRowMenu(canvas, "Administrator");
        await userEvent.click(await screen.findByRole("menuitem", { name: "View" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByRole("heading", { name: "Administrator" });
        expect(dialog.getByText("Type")).toBeInTheDocument();
        expect(dialog.getByRole("tab", { name: "Access" })).toBeInTheDocument();
        expect(dialog.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
        expect(dialog.getByRole("button", { name: "Close" })).toBeInTheDocument();
    },
};

export const EditRole: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await openRoleRowMenu(canvas, "HR Agent User");
        await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByRole("heading", { name: "Edit Role" });
        expect(dialog.getByLabelText(/Name/)).toHaveValue("HR Agent User");
    },
};

export const EditRoleSaveError: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    parameters: {
        msw: {
            handlers: withDefaults(
                http.put(`${ROLES_URL}/:id`, () => HttpResponse.json({ message: "The server rejected the update.", errorId: "err-role-save" }, { status: 500 })),
                ...rbacHandlers
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await openRoleRowMenu(canvas, "HR Agent User");
        await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByRole("heading", { name: "Edit Role" });
        await userEvent.click(dialog.getByRole("button", { name: "Save" }));

        const banner = await dialog.findByTestId("messageBanner");
        expect(banner).toHaveTextContent("The server rejected the update.");
        expect(screen.getByRole("dialog")).toBeInTheDocument();
    },
};

export const DeleteRole: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await openRoleRowMenu(canvas, "HR Agent User");
        await userEvent.click(await screen.findByRole("menuitem", { name: "Delete" }));

        const dialog = within(await screen.findByRole("dialog"));
        await dialog.findByText(/will be permanently deleted/);
        expect(dialog.getByText("HR Agent User")).toBeInTheDocument();
        expect(dialog.getByRole("button", { name: "Delete" })).toBeInTheDocument();
    },
};

export const DefaultsFailToLoad: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(DEFAULT_ROLES_URL, () => HttpResponse.json({ message: "Database unavailable", errorId: "e-1" }, { status: 503 })),
                ...rbacHandlers
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText(/The default roles could not be loaded/)).toBeInTheDocument();

        await openRoleRowMenu(canvas, "HR Agent User");
        expect(await screen.findByRole("menuitem", { name: "Add to Default Roles" })).toHaveAttribute("aria-disabled", "true");
    },
};

export const DefaultsSetInConfiguration: Story = {
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(DEFAULT_ROLES_URL, () => HttpResponse.json({ data: { roleIds: SEED_DEFAULT_ROLE_IDS, origin: "yaml" } })),
                ...rbacHandlers
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await openRoleRowMenu(canvas, "HR Agent User");
        await userEvent.click(await screen.findByRole("menuitem", { name: "Add to Default Roles" }));

        const dialog = within(await screen.findByRole("dialog"));
        expect(dialog.getByText("Default Roles Set in Configuration")).toBeInTheDocument();
        expect(dialog.getByText(/come from the Agent Mesh configuration/)).toBeInTheDocument();
    },
};
