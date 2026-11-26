// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { mockProjects } from "@/testing/data/projects";

import { ProjectsPage } from "./ProjectsPage";

const meta = {
    title: "Projects/ProjectsPage",
    component: ProjectsPage,
    parameters: {
        docs: {
            description: {
                component: "The main projects page component that displays project cards, search functionality, and handles project management interactions.",
            },
        },
    },
} satisfies Meta<typeof ProjectsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        projectContext: {
            projects: mockProjects,
            filteredProjects: mockProjects,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByTestId("refreshProjects")).toBeVisible();
        expect(await canvas.findByTestId("createProjectCard")).toBeVisible();
        for (const project of mockProjects) {
            expect(await canvas.findByText(project.name)).toBeVisible();
        }
    },
};

export const WithSearchTerm: Story = {
    parameters: {
        projectContext: {
            projects: mockProjects,
            filteredProjects: [mockProjects.find(p => p.name === "Weather App")!],
            searchQuery: "Weather",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const searchInput = await canvas.findByTestId("projectSearchInput");
        searchInput.focus();

        expect(await canvas.findByTestId("createProjectCard")).toBeVisible();
    },
};

export const WithNoResults: Story = {
    parameters: {
        projectContext: {
            projects: mockProjects,
            filteredProjects: [],
            searchQuery: "Nonexistent Project",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("No Projects Match Your Filter")).toBeVisible();
        expect(await canvas.findByTestId("Clear Filter")).toBeVisible();
    },
};

export const NoProjects: Story = {
    parameters: {
        projectContext: {},
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByTestId("Create New Project")).toBeVisible();
    },
};

export const Loading: Story = {
    parameters: {
        projectContext: {
            isLoading: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Loading projects...")).toBeVisible();
    },
};
