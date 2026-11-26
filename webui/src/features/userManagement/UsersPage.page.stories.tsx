// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { FULL_RBAC_SEED, seedRbacForStory } from "@/testing/data/rbac";
import { withDefaults } from "@/testing/helpers";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";

import { ClaimMappingPage } from "./ClaimMappingPage";
import { RolesPage } from "./RolesPage";
import { USER_MANAGEMENT_ROUTES } from "./routes";
import { UserManagementLayout } from "./UserManagementLayout";
import { UsersPage } from "./UsersPage";

const childRoutes = [
    { path: "roles", element: <RolesPage /> },
    { path: "users", element: <UsersPage /> },
    { path: "claim-mapping", element: <ClaimMappingPage /> },
];

const routerValues = (initialPath: string) => ({ routePath: USER_MANAGEMENT_ROUTES.base, children: childRoutes, initialPath });

const meta = {
    title: "User Management/Page",
    component: UserManagementLayout,
    parameters: {
        userContext: { userHasCapability: (capability: string) => capability !== CAPABILITIES.readBudgets },
        msw: { handlers: withDefaults(...rbacHandlers) },
    },
    beforeEach: () => seedRbacForStory(FULL_RBAC_SEED),
} satisfies Meta<typeof UserManagementLayout>;

export default meta;
type Story = StoryObj<typeof meta>;

export const RolesTab: Story = {
    parameters: { routerValues: routerValues(USER_MANAGEMENT_ROUTES.roles) },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Administrator");
        expect(canvas.getByRole("button", { name: "Manage Default Roles" })).toBeInTheDocument();
    },
};

export const UsersTab: Story = {
    parameters: { routerValues: routerValues(USER_MANAGEMENT_ROUTES.users) },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Alice Anderson");
        expect(canvas.queryByRole("button", { name: "Manage Default Roles" })).not.toBeInTheDocument();
    },
};

export const ClaimsTab: Story = {
    parameters: { routerValues: routerValues(USER_MANAGEMENT_ROUTES.claimMapping) },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("1dca4db7-9ff1-4fd5-bc28-93755f43c828");
        expect(canvas.queryByRole("button", { name: "Set Claim Key" })).not.toBeInTheDocument();
        expect(canvas.queryByRole("button", { name: "Manage Default Roles" })).not.toBeInTheDocument();
    },
};
