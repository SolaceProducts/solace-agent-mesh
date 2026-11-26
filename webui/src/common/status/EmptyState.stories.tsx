// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { EmptyState } from "./EmptyState";

const meta = {
    title: "Common/EmptyState",
    component: EmptyState,
    parameters: {
        docs: { description: { component: "Empty state used by pages with no data, errors, or loading conditions." } },
    },
} satisfies Meta<typeof EmptyState>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Error: Story = {
    args: {
        title: "Unable to retrieve data",
        subtitle: "Something went wrong.",
        variant: "error",
        buttons: [{ text: "Go Back", variant: "default", onClick: fn() }],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Unable to retrieve data")).toBeInTheDocument();
        await userEvent.click(canvas.getByRole("button", { name: "Go Back" }));
        expect(args.buttons![0].onClick).toHaveBeenCalled();
    },
};

export const NotFound: Story = {
    args: {
        title: "No results",
        subtitle: "We couldn't find what you were looking for",
        variant: "notFound",
        buttons: [{ text: "Go Back", variant: "default", onClick: fn() }],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("No results")).toBeInTheDocument();
        await userEvent.click(canvas.getByRole("button", { name: "Go Back" }));
        expect(args.buttons![0].onClick).toHaveBeenCalled();
    },
};

export const Loading: Story = {
    args: {
        title: "Loading data...",
        subtitle: "Hang tight",
        variant: "loading",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Loading data...")).toBeInTheDocument();
    },
};

export const NoImage: Story = {
    args: {
        title: "No image",
        subtitle: "This message has no image",
        variant: "noImage",
        buttons: [{ text: "Go Back", variant: "default", onClick: fn() }],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("No image")).toBeInTheDocument();
        await userEvent.click(canvas.getByRole("button", { name: "Go Back" }));
        expect(args.buttons![0].onClick).toHaveBeenCalled();
    },
};
