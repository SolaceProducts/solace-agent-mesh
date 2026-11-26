// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { Toast } from "./Toast";

const meta = {
    title: "Common/Toast",
    component: Toast,
    parameters: {
        docs: { description: { component: "Brief, dismissible message displayed to the user." } },
    },
} satisfies Meta<typeof Toast>;

export default meta;
type Story = StoryObj<typeof meta>;

export const AllVariants: Story = {
    args: { id: "ignored", message: "ignored" },
    render: () => (
        <div className="flex flex-col gap-4 p-4">
            <Toast id="default-toast" message="Default toast message" />
            <Toast id="success-toast" message="Success toast message" type="success" />
            <Toast id="warning-toast" message="Warning toast message" type="warning" />
        </div>
    ),
    play: async () => {
        const alerts = await screen.findAllByRole("alert");
        expect(alerts).toHaveLength(3);

        const [defaultAlert, successAlert, warningAlert] = alerts;
        expect(defaultAlert).toHaveTextContent("Default toast message");
        expect(successAlert).toHaveTextContent("Success toast message");
        expect(warningAlert).toHaveTextContent("Warning toast message");

        expect(defaultAlert.querySelector("svg")).toBeNull();
        expect(successAlert.querySelector("svg")).not.toBeNull();
        expect(warningAlert.querySelector("svg")).not.toBeNull();
    },
};
