// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps, useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { MultiSelect } from "./MultiSelect";

type MultiSelectProps = ComponentProps<typeof MultiSelect>;

const meta = {
    title: "Common/MultiSelect",
    component: MultiSelect,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Multi-select dropdown. Default renders removable chips; `summaryMode` shows a compact summary trigger and `emptyMeansAll` treats an empty selection as 'all' (filter semantics). `renderOption` customizes each row.",
            },
        },
    },
} satisfies Meta<typeof MultiSelect>;

export default meta;
type Story = StoryObj<typeof meta>;

function Controlled(args: MultiSelectProps) {
    const [value, setValue] = useState<string[]>(args.value ?? []);
    return (
        <div className="max-w-sm">
            <MultiSelect {...args} value={value} onChange={setValue} />
        </div>
    );
}

export const Chips: Story = {
    args: { options: ["file", "text", "image"], value: [], onChange: () => {}, placeholder: "Select modes..." },
    render: args => <Controlled {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("button", { name: "Select modes..." }));
        await userEvent.click(await screen.findByRole("button", { name: "file" }));
        const remove = await canvas.findByRole("button", { name: "Remove" });
        await expect(remove).toBeInTheDocument();
    },
};

export const FilterSummary: Story = {
    args: { options: ["alpha", "beta", "gamma"], value: [], onChange: () => {}, placeholder: "All items", summaryMode: true, emptyMeansAll: true },
    render: args => <Controlled {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const trigger = canvas.getByRole("button", { name: "All items" });
        await userEvent.click(trigger);
        await userEvent.click(await screen.findByRole("button", { name: "alpha" }));
        await expect(await canvas.findByText("2 selected")).toBeInTheDocument();
    },
};

export const CustomOptionRendering: Story = {
    args: {
        options: ["red", "green", "blue"],
        value: [],
        onChange: () => {},
        placeholder: "Pick colors...",
        renderOption: (option: string) => (
            <span className="flex items-center gap-2">
                <span className="inline-block size-3 rounded-full" style={{ backgroundColor: option }} />
                {option}
            </span>
        ),
    },
    render: args => <Controlled {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("button", { name: "Pick colors..." }));
        await expect(await screen.findByRole("button", { name: "green" })).toBeInTheDocument();
    },
};

const LONG_LABEL = "Team Role 12 maximum-length name for chip wrap testxxxxxxxxxx";
const longOptions = ["Short One", LONG_LABEL, "Another Short", `${LONG_LABEL} B`];
export const LongLabels: Story = {
    args: { options: longOptions, value: [longOptions[0], longOptions[1], longOptions[3]], onChange: () => {}, placeholder: "Select roles..." },
    render: args => <Controlled {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const chip = canvas.getByTitle(LONG_LABEL);
        await expect(chip).toBeInTheDocument();
        await expect(chip).toHaveClass("truncate");

        await userEvent.click(canvas.getByRole("button", { expanded: false }));
        const option = await screen.findByRole("button", { name: LONG_LABEL });
        await expect(within(option).getByText(LONG_LABEL)).toHaveClass("truncate");
    },
};

const manyOptions = Array.from({ length: 30 }, (_, i) => `Option ${String(i + 1).padStart(2, "0")}`);
export const ScrollableMenu: Story = {
    args: { options: manyOptions, value: [], onChange: () => {}, placeholder: "Select..." },
    render: args => <Controlled {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("button", { name: "Select..." }));
        const firstOption = await screen.findByRole("button", { name: "Option 01" });
        const menu = firstOption.parentElement as HTMLElement;
        await waitFor(() => expect(menu.scrollHeight).toBeGreaterThan(menu.clientHeight));
    },
};

export const Disabled: Story = {
    args: { options: ["file", "text"], value: ["file"], onChange: () => {}, placeholder: "Select modes...", disabled: true },
    render: args => <Controlled {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const trigger = canvas.getByRole("button");
        await expect(trigger).toHaveAttribute("aria-disabled", "true");
        await userEvent.click(trigger);
        await expect(canvas.queryByRole("button", { name: "text" })).not.toBeInTheDocument();
    },
};
