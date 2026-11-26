// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { useForm } from "react-hook-form";
import { expect, within } from "storybook/test";

import { FormFieldLayoutItem } from "../layout/FormFieldLayoutItem";

import { PasswordInput } from "./PasswordInput";

const meta = {
    title: "Common/PasswordInput",
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Password input component with show/hide toggle button. Uses react-hook-form Controller internally. Typically wrapped with FormFieldLayoutItem for form context.",
            },
        },
    },
} satisfies Meta;

export default meta;
type Story = StoryObj<typeof meta>;

function BasicStory() {
    const { control } = useForm({ defaultValues: { password: "" } });
    return (
        <div className="max-w-md">
            <FormFieldLayoutItem label="Password" required>
                <PasswordInput name="password" control={control} placeholder="Enter password" />
            </FormFieldLayoutItem>
        </div>
    );
}

function WithHelpTextStory() {
    const { control } = useForm({ defaultValues: { newPassword: "" } });
    return (
        <div className="max-w-md">
            <FormFieldLayoutItem label="New Password" required helpText="Must be at least 8 characters">
                <PasswordInput name="newPassword" control={control} placeholder="Enter new password" />
            </FormFieldLayoutItem>
        </div>
    );
}

function WithErrorStory() {
    const { control } = useForm({ defaultValues: { password: "" } });
    return (
        <div className="max-w-md">
            <FormFieldLayoutItem label="Password" required error={{ message: "Password is required" }}>
                <PasswordInput name="password" control={control} placeholder="Enter password" invalid />
            </FormFieldLayoutItem>
        </div>
    );
}

function DisabledStory() {
    const { control } = useForm({ defaultValues: { password: "" } });
    return (
        <div className="max-w-md">
            <FormFieldLayoutItem label="Password" required>
                <PasswordInput name="password" control={control} placeholder="Enter password" disabled />
            </FormFieldLayoutItem>
        </div>
    );
}

function WithStoredValueStory() {
    const { control } = useForm({ defaultValues: { password: "" } });
    return (
        <div className="max-w-md">
            <FormFieldLayoutItem label="API Key" helpText="A stored credential exists. Leave empty to keep the current value.">
                <PasswordInput name="password" control={control} hasStoredValue placeholder="Enter new API key" />
            </FormFieldLayoutItem>
        </div>
    );
}

function StandaloneStory() {
    const { control } = useForm({ defaultValues: { password: "" } });
    return (
        <div className="max-w-md space-y-4">
            <p className="text-sm">Password input can also be used standalone without FormFieldLayoutItem:</p>
            <PasswordInput name="password" control={control} placeholder="Enter password" />
        </div>
    );
}

export const Basic: Story = {
    render: () => <BasicStory />,
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        const input = await canvas.findByPlaceholderText("Enter password");

        await expect(input).toBeInTheDocument();
        await expect(input).toHaveAttribute("type", "password");

        const toggleButton = within(input.parentElement!).getByRole("button");
        await expect(toggleButton).toBeInTheDocument();
    },
};

export const WithHelpText: Story = {
    render: () => <WithHelpTextStory />,
};

export const WithError: Story = {
    render: () => <WithErrorStory />,
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        const input = await canvas.findByPlaceholderText("Enter password");

        await expect(input).toHaveAttribute("aria-invalid", "true");

        await expect(canvas.getByText("Password is required")).toBeInTheDocument();
    },
};

export const Disabled: Story = {
    render: () => <DisabledStory />,
};

export const WithStoredValue: Story = {
    render: () => <WithStoredValueStory />,
};

export const Standalone: Story = {
    render: () => <StandaloneStory />,
};
