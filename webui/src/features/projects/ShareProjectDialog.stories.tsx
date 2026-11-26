// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import type { ProjectSharesResponse } from "@/types/projects";

import { mockEmptyProject, mockProject } from "@/testing/data/projects";
import { withDefaults } from "@/testing/helpers";
import { withIdentityService, withoutIdentityService } from "@/testing/helpers/parameters";

import { ShareProjectDialog } from "./ShareProjectDialog";

const mockSharesResponse: ProjectSharesResponse = {
    projectId: "project-1",
    ownerEmail: "owner@example.com",
    shares: [
        {
            id: "share-1",
            projectId: "project-1",
            userEmail: "viewer1@example.com",
            accessLevel: "resource_viewer",
            sharedByEmail: "owner@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        },
    ],
};

const mockEmptySharesResponse: ProjectSharesResponse = {
    projectId: "project-empty",
    ownerEmail: "owner@example.com",
    shares: [],
};

const mockPeopleSearchResponse = {
    data: [
        { id: "person-1", workEmail: "alice@example.com" },
        { id: "person-2", workEmail: "bob@example.com" },
        { id: "person-3", workEmail: "charlie@example.com" },
    ],
};

const baseHandlers = withDefaults(
    http.get("*/api/v1/projects/*/shares", () => {
        return HttpResponse.json(mockSharesResponse);
    })
);

const emptySharesHandlers = withDefaults(
    http.get("*/api/v1/projects/project-empty/shares", () => {
        return HttpResponse.json(mockEmptySharesResponse);
    })
);

const withPeopleSearchHandlers = withDefaults(
    http.get("*/api/v1/projects/*/shares", () => {
        return HttpResponse.json(mockSharesResponse);
    }),
    http.get("*/api/v1/identity/people*", () => {
        return HttpResponse.json(mockPeopleSearchResponse);
    })
);

const meta = {
    title: "Projects/ShareProjectDialog",
    component: ShareProjectDialog,
    parameters: {
        docs: {
            description: {
                component: "Dialog for sharing a project with other users. Supports both identity service search (with UserTypeahead) and manual email entry modes.",
            },
        },
        msw: { handlers: baseHandlers },
    },
} satisfies Meta<typeof ShareProjectDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Dialog will close"),
        project: mockProject,
    },
    parameters: {
        msw: { handlers: baseHandlers },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByText("Share Project")).toBeInTheDocument();
        expect(await dialogContent.findByText(/Test Project/)).toBeInTheDocument();

        expect(await dialogContent.findByText("owner@example.com")).toBeInTheDocument();

        const addButton = await dialogContent.findByRole("button", { name: /Add/ });
        expect(addButton).toBeEnabled();
    },
};

export const WithIdentityService: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Dialog will close"),
        project: mockProject,
    },
    parameters: {
        ...withIdentityService("okta"),
        msw: { handlers: withPeopleSearchHandlers },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        const addButton = await dialogContent.findByRole("button", { name: /Add/ });
        await userEvent.click(addButton);

        const searchInput = await dialogContent.findByPlaceholderText("Search by email...");
        expect(searchInput).toBeInTheDocument();
    },
};

export const WithoutIdentityService: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Dialog will close"),
        project: mockProject,
    },
    parameters: {
        ...withoutIdentityService,
        msw: { handlers: baseHandlers },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        const addButton = await dialogContent.findByRole("button", { name: /Add/ });
        await userEvent.click(addButton);

        const emailInput = await dialogContent.findByPlaceholderText(/enter email/i);
        expect(emailInput).toBeInTheDocument();
    },
};

export const WithExistingShares: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Dialog will close"),
        project: mockProject,
    },
    parameters: {
        msw: { handlers: baseHandlers },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByText("owner@example.com")).toBeInTheDocument();
        const ownerBadge = await dialogContent.findByText("Owner");
        expect(ownerBadge).toBeInTheDocument();

        expect(await dialogContent.findByText("viewer1@example.com")).toBeInTheDocument();
        const [shareRow] = await dialogContent.findAllByTestId("share-row");
        expect(within(shareRow).getByRole("combobox")).toHaveTextContent(/viewer/i);
    },
};

export const EmptyState: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Dialog will close"),
        project: mockEmptyProject,
    },
    parameters: {
        msw: { handlers: emptySharesHandlers },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByText("owner@example.com")).toBeInTheDocument();
        expect(await dialogContent.findByText("Owner")).toBeInTheDocument();

        expect(dialogContent.queryByText("viewer1@example.com")).toBeNull();
    },
};
