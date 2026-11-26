// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { InlineNotice } from "./InlineNotice";

const meta = {
    title: "Common/InlineNotice",
    component: InlineNotice,
    decorators: [
        Story => (
            <div className="max-w-2xl">
                <Story />
            </div>
        ),
    ],
    parameters: {
        layout: "padded",
        docs: {
            description: { component: "Shows what happened to one turn inline in a transcript: a warning for a budget refusal, an error for a failure" },
        },
    },
} satisfies Meta<typeof InlineNotice>;

export default meta;
type Story = StoryObj<typeof meta>;

const WARNING_TEXT = "Sorry, I can't take new requests from you right now: your usage budget is used up until Nov 1.";
const ERROR_TEXT = "An unexpected error occurred while processing your request.";

export const Warning: Story = {
    args: { variant: "warning", children: WARNING_TEXT },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByRole("status")).toHaveTextContent(WARNING_TEXT);
        expect(canvas.queryByRole("alert")).not.toBeInTheDocument();
    },
};

export const BoxedError: Story = {
    args: { variant: "error", children: ERROR_TEXT, boxed: true },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByRole("alert")).toHaveTextContent(ERROR_TEXT);
        expect(canvas.queryByRole("status")).not.toBeInTheDocument();
    },
};
