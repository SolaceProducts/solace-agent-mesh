// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { BuilderAccessDenied } from "@/features/builder/BuilderAccessDenied";

const meta = {
    title: "Builder/BuilderAccessDenied",
    component: BuilderAccessDenied,
    parameters: {
        docs: { description: { component: "Served by the router in place of BuilderPage when the user lacks the `builder:_:use` scope." } },
    },
} satisfies Meta<typeof BuilderAccessDenied>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Access Denied")).toBeInTheDocument();
        expect(canvas.getByText(/Contact your administrator/i)).toBeInTheDocument();
    },
};
