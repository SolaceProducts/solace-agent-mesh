// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps, useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent, within } from "storybook/test";

import { comboBoxMockData } from "@/testing/helpers/comboBoxItems";

import { ComboBox } from "./combobox";

type ComboBoxProps = ComponentProps<typeof ComboBox>;

const meta = {
    title: "UI/ComboBox",
    component: ComboBox,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "A customizable combobox component with search, keyboard navigation, and grouped items support.",
            },
        },
    },
} satisfies Meta<typeof ComboBox>;

export default meta;
type Story = StoryObj<typeof meta>;

function ControlledComboBox(args: ComboBoxProps) {
    const [selected, setSelected] = useState<string | undefined>(args.value);

    return (
        <div className="max-w-sm">
            <ComboBox
                {...args}
                value={selected}
                onValueChange={value => {
                    setSelected(value);
                    args.onValueChange?.(value);
                }}
            />
        </div>
    );
}

export const Basic: Story = {
    args: {
        items: comboBoxMockData.basicItems,
        placeholder: "Select an option...",
        onValueChange: fn(),
        onOpen: fn(),
    },
    render: args => <ControlledComboBox {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const input = await canvas.findByPlaceholderText("Select an option...");

        await userEvent.click(input);

        await expect(await screen.findByText("First Option")).toBeInTheDocument();
        await expect(await screen.findByText("Second Option")).toBeInTheDocument();
        await expect(await screen.findByText("Third Option")).toBeInTheDocument();
    },
};

export const DefaultSelection: Story = {
    args: {
        value: "option-2",
        items: comboBoxMockData.basicItems,
        placeholder: "Select an option...",
        onValueChange: () => {},
    },
    render: args => <ControlledComboBox {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const input = await canvas.findByDisplayValue("Second Option");

        await expect(input).toBeInTheDocument();
    },
};

export const WithSubtext: Story = {
    args: {
        items: comboBoxMockData.itemsWithSubtext,
        placeholder: "Select a language...",
        onValueChange: () => {},
    },
    render: args => <ControlledComboBox {...args} />,
};

export const WithSections: Story = {
    args: {
        items: comboBoxMockData.itemsWithSections,
        placeholder: "Select a model...",
        onValueChange: fn(),
        onOpen: fn(),
    },
    render: args => <ControlledComboBox {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const input = await canvas.findByPlaceholderText("Select a model...");

        await userEvent.click(input);

        await expect(await screen.findByText("Common Model")).toBeInTheDocument();
        await expect(await screen.findByText("General Purpose")).toBeInTheDocument();

        await expect(await screen.findByText("Specialized Model")).toBeInTheDocument();
        await expect(await screen.findByText("Research Model")).toBeInTheDocument();
    },
};

export const Loading: Story = {
    args: {
        items: [],
        placeholder: "Loading options...",
        isLoading: true,
        onValueChange: fn(),
    },
    render: args => <ControlledComboBox {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const input = await canvas.findByPlaceholderText("Loading options...");

        await expect(input).toBeEnabled();

        const spinner = canvasElement.querySelector('[class*="animate-spin"]');
        await expect(spinner).toBeInTheDocument();
    },
};

export const NoItemsFound: Story = {
    args: {
        items: [],
        placeholder: "Type or select a model...",
        noItemsFoundText: "No models found",
        allowCustomValue: true,
        onValueChange: fn(),
    },
    render: args => <ControlledComboBox {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const input = await canvas.findByPlaceholderText("Type or select a model...");

        await userEvent.type(input, " ");

        await expect(await screen.findByText("No models found")).toBeInTheDocument();
    },
};

export const Disabled: Story = {
    args: {
        value: "option-2",
        items: comboBoxMockData.basicItems,
        placeholder: "Select an option...",
        disabled: true,
        onValueChange: fn(),
    },
    render: args => <ControlledComboBox {...args} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const input = await canvas.findByDisplayValue("Second Option");

        await expect(input).toBeDisabled();
    },
};

export const WithCustomRendering: Story = {
    args: {
        items: comboBoxMockData.itemsWithImages,
        placeholder: "Select a provider...",
        onValueChange: fn(),
        renderItem: item => (
            <div className="flex items-center gap-3">
                <div className="flex h-6 w-6 items-center justify-center rounded-full bg-linear-to-br from-blue-400 to-blue-600 text-xs font-bold text-white">{item.label[0]}</div>
                <div>
                    <div className="font-medium">{item.label}</div>
                    {item.subtext && <div className="text-xs text-gray-500">{item.subtext}</div>}
                </div>
            </div>
        ),
    },
    render: args => <ControlledComboBox {...args} />,
};
