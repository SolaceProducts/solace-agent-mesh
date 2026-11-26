// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";

import { type RadioOption, RadioOptions } from "./RadioOptions";

const ACCESS_OPTIONS: readonly RadioOption[] = [
    { value: "read", label: "Read" },
    { value: "write", label: "Write" },
    { value: "admin", label: "Admin" },
];

const EVALUATION_OPTIONS: readonly RadioOption[] = [
    {
        value: "create_run",
        label: "Create and run experiments",
        description: "Users can create experiments for agents they have access to. They can run, update or delete their own experiments, and view experiment reports.",
    },
    {
        value: "view",
        label: "View experiments and reports only",
        description: "Users can view experiments and reports for agents they have access to.",
    },
];

const meta = {
    title: "Common/RadioOptions",
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: 'Controlled, horizontal, label-only radio group over native `<input type="radio">`. One option is selected at a time. Used in connector and gateway forms.',
            },
        },
    },
} satisfies Meta;

export default meta;
type Story = StoryObj<typeof meta>;

function Controlled({
    options = ACCESS_OPTIONS,
    initialValue = "",
    disabled,
    orientation,
    deselectable,
}: {
    options?: readonly RadioOption[];
    initialValue?: string;
    disabled?: boolean;
    orientation?: "horizontal" | "vertical";
    deselectable?: boolean;
}) {
    const [value, setValue] = useState(initialValue);
    return (
        <div className="max-w-md space-y-3">
            <RadioOptions options={options} value={value} onChange={setValue} disabled={disabled} orientation={orientation} deselectable={deselectable} />
            <p className="text-sm text-(--secondary-text-wMain)">Selected: {value || "(none)"}</p>
        </div>
    );
}

export const Default: Story = {
    render: () => <Controlled />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const write = canvas.getByRole("radio", { name: "Write" });

        await expect(write).not.toBeChecked();
        await userEvent.click(write);
        await expect(write).toBeChecked();
        await expect(canvas.getByText("Selected: write")).toBeInTheDocument();
    },
};

export const Preselected: Story = {
    render: () => <Controlled initialValue="write" />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByRole("radio", { name: "Write" })).toBeChecked();
    },
};

export const Disabled: Story = {
    render: () => <Controlled initialValue="read" disabled />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        for (const option of ACCESS_OPTIONS) {
            await expect(canvas.getByRole("radio", { name: option.label })).toBeDisabled();
        }
    },
};

export const Vertical: Story = {
    render: () => <Controlled orientation="vertical" />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const admin = canvas.getByRole("radio", { name: "Admin" });

        await userEvent.click(admin);
        await expect(admin).toBeChecked();
        await expect(canvas.getByText("Selected: admin")).toBeInTheDocument();
    },
};

export const WithDescriptions: Story = {
    render: () => <Controlled options={EVALUATION_OPTIONS} orientation="vertical" deselectable />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const createRun = canvas.getByRole("radio", { name: /Create and run experiments/ });

        await expect(canvas.getByText(/view experiment reports/)).toBeInTheDocument();
        await userEvent.click(createRun);
        await expect(createRun).toBeChecked();
        await expect(canvas.getByText("Selected: create_run")).toBeInTheDocument();
    },
};

export const Deselectable: Story = {
    render: () => <Controlled deselectable orientation="vertical" />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const read = canvas.getByRole("radio", { name: "Read" });

        await userEvent.click(read);
        await expect(read).toBeChecked();

        await userEvent.click(read);
        await expect(read).not.toBeChecked();
        await expect(canvas.getByText("Selected: (none)")).toBeInTheDocument();
    },
};
