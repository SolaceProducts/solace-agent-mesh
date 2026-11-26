// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fireEvent, fn, screen, within } from "storybook/test";

import { mockToolsetFiles } from "@/testing/data";

import { ToolsetToolCards } from "./ToolsetToolCards";

const meta = {
    title: "Toolsets/ToolsetToolCards",
    component: ToolsetToolCards,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Renders a card for each discovered tool file. Shows an empty state when no tools are present.",
            },
        },
    },
    decorators: [
        Story => (
            <div className="w-150">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof ToolsetToolCards>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        files: mockToolsetFiles,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Query Customers")).toBeInTheDocument();
        expect(canvas.getByText("Transform Records")).toBeInTheDocument();
        expect(canvas.getByText("Export CSV")).toBeInTheDocument();

        expect(canvas.getByText("Fetches customer records from the CRM based on filter criteria such as region, tier, and account status.")).toBeInTheDocument();
        expect(canvas.getByText("Exports a dataset to a CSV file for download.")).toBeInTheDocument();

        expect(canvas.queryByText("No tools discovered yet")).not.toBeInTheDocument();
    },
};

export const EmptyState: Story = {
    args: {
        files: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("No tools discovered yet")).toBeInTheDocument();

        expect(canvas.queryByText("Query Customers")).not.toBeInTheDocument();
    },
};

export const SingleTool: Story = {
    args: {
        files: [mockToolsetFiles[0]],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Query Customers")).toBeInTheDocument();
        expect(canvas.getByText("Fetches customer records from the CRM based on filter criteria such as region, tier, and account status.")).toBeInTheDocument();

        expect(canvas.queryByText("Transform Records")).not.toBeInTheDocument();
        expect(canvas.queryByText("Export CSV")).not.toBeInTheDocument();

        expect(canvas.queryByText("No tools discovered yet")).not.toBeInTheDocument();
    },
};

const manyFiles = Array.from({ length: 12 }, (_, i) => ({
    id: `generated-file-${i}`,
    filename: `tool_${i}.py`,
    toolName: `Generated Tool ${String(i).padStart(2, "0")}`,
    toolFunction: `generated_tool_${i}`,
    toolDescription: `Auto-generated tool ${i} used for layout testing in storybook.`,
    timeoutSeconds: 30,
    sandboxProfile: "default",
    modulePath: `tools.generated_tool_${i}`,
    fileSize: 1024,
    configSchema: null,
    createdAt: "2026-01-01T00:00:00Z",
}));

export const ConfigCogOnToolWithSchema: Story = {
    args: {
        files: mockToolsetFiles,
        onConfigSave: fn(),
        configValues: {},
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const cogs = canvas.getAllByRole("button", { name: /Configure/i });
        expect(cogs).toHaveLength(1);

        fireEvent.click(cogs[0]);
        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Tool Configuration")).toBeInTheDocument();
    },
};

export const NoCogWithoutOnConfigSave: Story = {
    args: {
        files: mockToolsetFiles,
        onConfigSave: undefined,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.queryByRole("button", { name: /Configure/i })).not.toBeInTheDocument();
    },
};

export const ManyTools: Story = {
    args: {
        files: manyFiles,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Generated Tool 00")).toBeInTheDocument();
        expect(canvas.getByText("Generated Tool 11")).toBeInTheDocument();

        expect(canvas.queryByText("No tools discovered yet")).not.toBeInTheDocument();
    },
};
