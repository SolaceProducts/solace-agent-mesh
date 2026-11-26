// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { SortableTableHead } from "./table";

const meta = {
    title: "UI/SortableTableHead",
    component: SortableTableHead,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "The sortable table head component",
            },
        },
    },
    decorators: [
        (Story: React.ComponentType) => (
            <table>
                <thead>
                    <tr>
                        <Story />
                    </tr>
                </thead>
            </table>
        ),
    ],
} satisfies Meta<typeof SortableTableHead>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Inactive: Story = {
    args: {
        column: "name",
        currentSortKey: "other",
        sortDir: "asc",
        onSort: fn(),
        children: "Name",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Name")).toBeInTheDocument();
        expect(canvas.getByRole("button")).toBeInTheDocument();
        const svg = canvas.getByRole("button").querySelector("svg");
        expect(svg?.getAttribute("class")).toContain("opacity-40");
    },
};

export const ActiveAscending: Story = {
    args: {
        column: "name",
        currentSortKey: "name",
        sortDir: "asc",
        onSort: fn(),
        children: "Name",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const svg = canvas.getByRole("button").querySelector("svg");
        expect(svg?.getAttribute("class")).not.toContain("opacity-40");
    },
};

export const ActiveDescending: Story = {
    args: {
        column: "name",
        currentSortKey: "name",
        sortDir: "desc",
        onSort: fn(),
        children: "Name",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const svg = canvas.getByRole("button").querySelector("svg");
        expect(svg?.getAttribute("class")).not.toContain("opacity-40");
    },
};

export const ClickCallsOnSort: Story = {
    args: {
        column: "email",
        currentSortKey: "other",
        sortDir: "asc",
        onSort: fn(),
        children: "Email",
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("button"));
        expect(args.onSort).toHaveBeenCalledWith("email");
        expect(args.onSort).toHaveBeenCalledTimes(1);
    },
};

export const MultipleColumns: Story = {
    args: { column: "name", currentSortKey: "name", sortDir: "asc", onSort: fn(), children: "Name" },
    render: () => (
        <>
            <SortableTableHead column="name" currentSortKey="name" sortDir="asc" onSort={() => {}}>
                Name
            </SortableTableHead>
            <SortableTableHead column="model" currentSortKey="name" sortDir="asc" onSort={() => {}}>
                Model
            </SortableTableHead>
            <SortableTableHead column="provider" currentSortKey="name" sortDir="asc" onSort={() => {}}>
                Model Provider
            </SortableTableHead>
        </>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Name")).toBeInTheDocument();
        expect(canvas.getByText("Model")).toBeInTheDocument();
        expect(canvas.getByText("Model Provider")).toBeInTheDocument();

        const buttons = canvas.getAllByRole("button");
        expect(buttons).toHaveLength(3);

        const nameSvg = buttons[0].querySelector("svg");
        const modelSvg = buttons[1].querySelector("svg");
        expect(nameSvg?.getAttribute("class")).not.toContain("opacity-40");
        expect(modelSvg?.getAttribute("class")).toContain("opacity-40");
    },
};
