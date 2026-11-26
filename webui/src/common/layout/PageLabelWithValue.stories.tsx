// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { PageLabel } from "./PageLabel";
import { PageLabelWithValue } from "./PageLabelWithValue";

const meta = {
    title: "Common/Layout/PageLabelWithValue",
    component: PageLabelWithValue,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Grid layout pairing a label row above its value row.",
            },
        },
    },
} satisfies Meta<typeof PageLabelWithValue>;

export default meta;

export const Layout = {
    render: () => (
        <PageLabelWithValue>
            <PageLabel>Label goes here</PageLabel>
            <div className="h-8 w-100 border">Form control or value goes here</div>
        </PageLabelWithValue>
    ),
    play: async () => {
        await expect(screen.getByText("Label goes here")).toBeInTheDocument();
    },
};
