// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { emptyProject, projectWithLongDescription, projectWithManyArtifacts, weatherProject } from "@/testing/data/projects";

import { ProjectCard } from "./ProjectCard";

const meta = {
    title: "Projects/ProjectCard",
    component: ProjectCard,
    parameters: {
        layout: "centered",
        docs: {
            description: {
                component: "Card component for displaying project information in a grid layout. Shows project name, description, artifact count, and provides menu actions for owners.",
            },
        },
    },
} satisfies Meta<typeof ProjectCard>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        project: weatherProject,
        onClick: () => alert("Card clicked"),
        onDelete: () => alert("Delete clicked"),
        onExport: () => alert("Export clicked"),
    },
    play: async () => {
        expect(await screen.findByText(weatherProject.name)).toBeInTheDocument();
        expect(await screen.findByText(weatherProject.description!)).toBeInTheDocument();
        expect(await screen.findByText("5 files")).toBeInTheDocument();
        expect(await screen.findByRole("button", { name: "More options" })).toBeInTheDocument();
    },
};

export const WithDescription: Story = {
    args: {
        project: projectWithLongDescription,
        onClick: () => alert("Card clicked"),
        onDelete: () => alert("Delete clicked"),
        onExport: () => alert("Export clicked"),
    },
    play: async () => {
        expect(await screen.findByText(projectWithLongDescription.name)).toBeInTheDocument();
        const descriptionElement = await screen.findByText(projectWithLongDescription.description!);
        expect(descriptionElement).toBeInTheDocument();
    },
};

export const WithManyArtifacts: Story = {
    args: {
        project: projectWithManyArtifacts,
        onClick: () => alert("Card clicked"),
        onDelete: () => alert("Delete clicked"),
        onExport: () => alert("Export clicked"),
    },
    play: async () => {
        expect(await screen.findByText(projectWithManyArtifacts.name)).toBeInTheDocument();
        expect(await screen.findByText("157 files")).toBeInTheDocument();
    },
};

export const EmptyProject: Story = {
    args: {
        project: emptyProject,
        onClick: () => alert("Card clicked"),
        onDelete: () => alert("Delete clicked"),
        onExport: () => alert("Export clicked"),
    },
    play: async () => {
        expect(await screen.findByText(emptyProject.name)).toBeInTheDocument();
        expect(await screen.findByRole("button", { name: "More options" })).toBeInTheDocument();
    },
};
