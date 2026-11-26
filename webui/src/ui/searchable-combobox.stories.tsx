// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, within } from "storybook/test";

import { SearchableCombobox } from "./searchable-combobox";

interface Item {
    id: string;
    name: string;
}

const items: Item[] = [
    { id: "1", name: "Alpha" },
    { id: "2", name: "Beta" },
    { id: "3", name: "Gamma" },
    { id: "4", name: "Delta" },
    { id: "5", name: "Epsilon" },
];

interface StoryArgs {
    clearable?: boolean;
    disabled?: boolean;
    items: Item[];
    initialValue?: string;
}

function Harness({ clearable, disabled, items, initialValue }: StoryArgs) {
    const [value, setValue] = useState(initialValue ?? "");
    return (
        <div className="max-w-sm">
            <SearchableCombobox
                items={items}
                value={value}
                onChange={setValue}
                getId={i => i.id}
                getSearchText={i => i.name}
                renderItem={i => <span>{i.name}</span>}
                renderSelected={i => <span>{i.name}</span>}
                placeholder="Choose one"
                searchPlaceholder="Search…"
                noMatchText="No matches."
                disabled={disabled}
                clearable={clearable}
                data-testid="storyCombobox"
            />
        </div>
    );
}

const meta = {
    title: "UI/SearchableCombobox",
    component: Harness,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Generic searchable dropdown — click to open, type to filter, click an item to select. Optional ✕ clears the selection.",
            },
        },
    },
} satisfies Meta<typeof Harness>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: { items },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const trigger = canvas.getByTestId("storyCombobox");
        expect(trigger).toHaveTextContent("Choose one");

        await userEvent.click(trigger);
        await screen.findByPlaceholderText("Search…");

        await userEvent.click(await screen.findByRole("button", { name: "Beta" }));
        expect(trigger).toHaveTextContent("Beta");
    },
};

export const SearchFilters: Story = {
    args: { items },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(canvas.getByTestId("storyCombobox"));
        await userEvent.type(await screen.findByPlaceholderText("Search…"), "ep");

        expect(await screen.findByRole("button", { name: "Epsilon" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Alpha" })).not.toBeInTheDocument();
    },
};

export const NoMatch: Story = {
    args: { items },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(canvas.getByTestId("storyCombobox"));
        await userEvent.type(await screen.findByPlaceholderText("Search…"), "zzz");

        expect(await screen.findByText("No matches.")).toBeInTheDocument();
    },
};

export const Clearable: Story = {
    args: { items, clearable: true, initialValue: "1" },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const trigger = canvas.getByTestId("storyCombobox");
        expect(trigger).toHaveTextContent("Alpha");

        await userEvent.click(canvas.getByRole("button", { name: /clear selection/i }));
        expect(trigger).toHaveTextContent("Choose one");
    },
};

export const Disabled: Story = {
    args: { items, disabled: true },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const trigger = canvas.getByTestId("storyCombobox");
        await userEvent.click(trigger);

        expect(screen.queryByPlaceholderText("Search…")).not.toBeInTheDocument();
    },
};

export const KeyboardOpensWithEnter: Story = {
    args: { items },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const trigger = canvas.getByTestId("storyCombobox");
        trigger.focus();
        await userEvent.keyboard("{Enter}");

        expect(await screen.findByPlaceholderText("Search…")).toBeInTheDocument();
    },
};
