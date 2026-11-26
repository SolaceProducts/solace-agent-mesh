// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { Metadata } from "./Metadata";

const meta = {
    title: "Common/Layout/Metadata",
    component: Metadata,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Fixed audit-field block for detail pages: created/updated by and at, plus the resource ID. Formats the `system` sentinel actor as `System` and shows `N/A` for absent timestamps.",
            },
        },
    },
} satisfies Meta<typeof Metadata>;

export default meta;

export const Default = {
    render: () => (
        <Metadata
            metadata={{
                id: "1b8f0c2a-4d3e-4f56-9a7b-0c1d2e3f4a5b",
                createdBy: "john.doe@example.org",
                createdAt: "2024-03-19T10:30:00Z",
                updatedBy: "jane.doe@example.org",
                updatedAt: "2024-03-19T14:45:00Z",
            }}
        />
    ),
    play: async () => {
        await expect(screen.getByText("Created By:")).toBeInTheDocument();
        await expect(screen.getByText("Created:")).toBeInTheDocument();
        await expect(screen.getByText("Last Updated By:")).toBeInTheDocument();
        await expect(screen.getByText("Last Updated:")).toBeInTheDocument();
        await expect(screen.getByText("ID:")).toBeInTheDocument();
        await expect(screen.getByText("john.doe@example.org")).toBeInTheDocument();
        await expect(screen.getByText("1b8f0c2a-4d3e-4f56-9a7b-0c1d2e3f4a5b")).toBeInTheDocument();
    },
};

export const SystemActor = {
    render: () => (
        <Metadata
            metadata={{
                id: "9c7d6e5f-4a3b-4c2d-8e1f-0a9b8c7d6e5f",
                createdBy: "system",
                createdAt: "2024-03-19T10:30:00Z",
                updatedBy: "system",
                updatedAt: "2024-03-19T14:45:00Z",
            }}
        />
    ),
    play: async () => {
        await expect(screen.getAllByText("System")).toHaveLength(2);
    },
};

export const WithScope = {
    render: () => (
        <Metadata
            scope="agent:1b8f0c2a-4d3e-4f56-9a7b-0c1d2e3f4a5b:invoke"
            metadata={{
                id: "1b8f0c2a-4d3e-4f56-9a7b-0c1d2e3f4a5b",
                createdBy: "john.doe@example.org",
                createdAt: "2024-03-19T10:30:00Z",
                updatedBy: "jane.doe@example.org",
                updatedAt: "2024-03-19T14:45:00Z",
            }}
        />
    ),
    play: async () => {
        await expect(screen.getByText("Invoke Scope:")).toBeInTheDocument();
        await expect(screen.getByText("agent:1b8f0c2a-4d3e-4f56-9a7b-0c1d2e3f4a5b:invoke")).toBeInTheDocument();
    },
};

export const MissingTimestamps = {
    render: () => (
        <Metadata
            metadata={{
                id: "0f1e2d3c-4b5a-4968-8776-655443322110",
                createdBy: "john.doe@example.org",
            }}
        />
    ),
    play: async () => {
        await expect(screen.getAllByText("N/A")).toHaveLength(2);
    },
};
