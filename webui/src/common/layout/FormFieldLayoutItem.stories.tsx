// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { Input } from "@/ui";

import { FormFieldLayoutItem } from "./FormFieldLayoutItem";

const meta = {
    title: "Common/Layout/FormFieldLayoutItem",
    component: FormFieldLayoutItem,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Form field wrapper that pairs a label with its input and renders error/warning/help text states beneath.",
            },
        },
    },
} satisfies Meta<typeof FormFieldLayoutItem>;

export default meta;

export const Basic = {
    render: () => (
        <>
            <h3 className="mb-4 text-sm font-semibold text-(--secondary-text-wMain)">FormFieldLayoutItem - Form field wrapper with label and validation</h3>
            <div className="max-w-md">
                <FormFieldLayoutItem label="Username" required>
                    <Input type="text" placeholder="Enter username" />
                </FormFieldLayoutItem>
            </div>
        </>
    ),
    play: async () => {
        await expect(screen.getByText("Username")).toBeInTheDocument();
        await expect(screen.getByPlaceholderText("Enter username")).toBeInTheDocument();
    },
};

export const WithError = {
    render: () => (
        <>
            <h3 className="mb-4 text-sm font-semibold text-(--secondary-text-wMain)">With Error State</h3>
            <div className="max-w-md">
                <FormFieldLayoutItem label="Email" required error={{ message: "Invalid email format" }}>
                    <Input type="email" placeholder="Enter email" />
                </FormFieldLayoutItem>
            </div>
        </>
    ),
    play: async () => {
        await expect(screen.getByText("Email")).toBeInTheDocument();
        await expect(screen.getByText("Invalid email format")).toBeInTheDocument();
    },
};

export const WithHelpText = {
    render: () => (
        <>
            <h3 className="mb-4 text-sm font-semibold text-(--secondary-text-wMain)">With Help Text</h3>
            <div className="max-w-md">
                <FormFieldLayoutItem label="Password" required helpText="Must be at least 8 characters">
                    <Input type="password" placeholder="Enter password" />
                </FormFieldLayoutItem>
            </div>
        </>
    ),
    play: async () => {
        await expect(screen.getByText("Password")).toBeInTheDocument();
        await expect(screen.getByText("Must be at least 8 characters")).toBeInTheDocument();
    },
};
