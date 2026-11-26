// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import type { ReactNode } from "react";
import { type DefaultValues, type FieldValues, FormProvider, useForm } from "react-hook-form";
import { expect, within } from "storybook/test";

import { KeyValuePairList } from "./KeyValuePairList";

function FormScope<T extends FieldValues>({ defaultValues, children }: { defaultValues: DefaultValues<T>; children: ReactNode }) {
    const form = useForm<T>({ defaultValues });
    return (
        <FormProvider {...form}>
            <form className="max-w-md">{children}</form>
        </FormProvider>
    );
}

const meta = {
    title: "Common/KeyValuePairList",
    component: KeyValuePairList,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "A reusable component for managing key-value pair inputs in forms using react-hook-form.",
            },
        },
    },
    decorators: [
        Story => (
            <FormScope defaultValues={{ pairs: [{ key: "", value: "" }] }}>
                <Story />
            </FormScope>
        ),
    ],
} satisfies Meta<typeof KeyValuePairList>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Basic: Story = {
    args: {
        name: "pairs",
        minPairs: 1,
    },
    render: args => (
        <div className="max-w-md">
            <div className="mb-2 space-y-2">
                <h3 className="text-sm font-semibold">Key-Value Pair List</h3>
            </div>
            <KeyValuePairList {...args} />
        </div>
    ),
    play: async ({ canvasElement }) => {
        const keyInputs = canvasElement.querySelectorAll('input[type="text"]');
        expect(keyInputs.length).toBeGreaterThanOrEqual(2);
    },
};

export const MultipleMinPairs: Story = {
    args: { name: "headers", minPairs: 2 },
    render: () => (
        <FormScope
            defaultValues={{
                headers: [
                    { key: "Content-Type", value: "application/json" },
                    { key: "Authorization", value: "Bearer token" },
                    { key: "", value: "" },
                ],
            }}
        >
            <div className="space-y-2">
                <h3 className="text-sm font-medium">HTTP Headers</h3>
                <KeyValuePairList name="headers" minPairs={2} />
            </div>
        </FormScope>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await expect(await canvas.findByText("HTTP Headers")).toBeInTheDocument();

        const inputs = canvasElement.querySelectorAll('input[type="text"]');
        expect(inputs.length).toBeGreaterThanOrEqual(4);
    },
};

export const WithError: Story = {
    args: { name: "pairs", minPairs: 1, error: { root: { message: "At least one key-value pair is required" } } },
    render: () => (
        <FormScope defaultValues={{ pairs: [{ key: "", value: "" }] }}>
            <KeyValuePairList name="pairs" minPairs={1} error={{ root: { message: "At least one key-value pair is required" } }} />
        </FormScope>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await expect(await canvas.findByText("At least one key-value pair is required")).toBeInTheDocument();
    },
};

export const EmptyState: Story = {
    args: { name: "pairs", minPairs: 1 },
    render: () => (
        <FormScope defaultValues={{ pairs: [] }}>
            <KeyValuePairList name="pairs" minPairs={1} />
        </FormScope>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(await canvas.findByText(/no items added yet/i)).toBeInTheDocument();

        const inputs = canvasElement.querySelectorAll('input[type="text"]');
        await expect(inputs.length).toBe(0);
    },
};

export const RemoveButtonBehavior: Story = {
    args: { name: "pairs", minPairs: 1 },
    render: () => (
        <FormScope
            defaultValues={{
                pairs: [
                    { key: "key1", value: "value1" },
                    { key: "key2", value: "value2" },
                    { key: "key3", value: "value3" },
                ],
            }}
        >
            <div className="space-y-2">
                <h3 className="text-sm font-medium">Remove pairs (only last pair can be removed)</h3>
                <KeyValuePairList name="pairs" minPairs={1} />
            </div>
        </FormScope>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const removeButtons = await canvas.findAllByRole("button", { name: /remove pair/i });
        await expect(removeButtons.length).toBe(3);

        const inputs = canvasElement.querySelectorAll('input[type="text"]');
        await expect(inputs.length).toBe(6);
    },
};

export const MinPairsEnforcement: Story = {
    args: { name: "headers", minPairs: 3 },
    render: () => (
        <FormScope
            defaultValues={{
                headers: [
                    { key: "Authorization", value: "Bearer token" },
                    { key: "Content-Type", value: "application/json" },
                    { key: "Accept", value: "application/json" },
                    { key: "User-Agent", value: "MyApp/1.0" },
                ],
            }}
        >
            <div className="space-y-2">
                <h3 className="text-sm font-medium">HTTP Headers (minimum 3 required)</h3>
                <KeyValuePairList name="headers" minPairs={3} />
            </div>
        </FormScope>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const removeButtons = await canvas.findAllByRole("button", { name: /remove pair/i });
        await expect(removeButtons.length).toBe(4);

        const inputs = canvasElement.querySelectorAll('input[type="text"]');
        await expect(inputs.length).toBe(8);
    },
};

export const KeyValueHeaderLabels: Story = {
    args: { name: "pairs", minPairs: 1 },
    render: () => (
        <FormScope
            defaultValues={{
                pairs: [
                    { key: "name", value: "John" },
                    { key: "age", value: "30" },
                ],
            }}
        >
            <div className="space-y-2">
                <h3 className="text-sm font-medium">Key-Value headers display</h3>
                <KeyValuePairList name="pairs" minPairs={1} />
            </div>
        </FormScope>
    ),
    play: async ({ canvasElement }) => {
        const allText = canvasElement.textContent;
        await expect(allText).toContain("Key");
        await expect(allText).toContain("Value");
    },
};
