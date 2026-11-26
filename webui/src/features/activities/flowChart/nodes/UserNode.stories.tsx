// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import UserNode from "@/features/activities/flowChart/nodes/UserNode";

import { createActivityLayoutNode } from "@/testing/helpers/layoutNodes";

const meta = {
    title: "Activities/ActivitiesVisualization/UserNode",
    component: UserNode,
    parameters: {
        layout: "padded",
    },
    decorators: [
        Story => (
            <div className="flex">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof UserNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const baseNode = createActivityLayoutNode({
    id: "user-1",
    type: "user",
    width: 100,
    height: 50,
    data: { label: "User" },
});

export const Default: Story = {
    args: { node: baseNode },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("User")).toBeInTheDocument();
        expect(canvasElement.querySelector('[data-testid="userNode"]')).toBeInTheDocument();
    },
};

export const Wiring: Story = {
    args: { node: baseNode, isSelected: true, onClick: fn() },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const label = await canvas.findByText("User");
        const clickable = label.closest("[role='button']") as HTMLElement;
        expect(clickable.className).toMatch(/!border-\(--accent-n2-wMain\)/);
        await userEvent.click(clickable);
        expect(args.onClick).toHaveBeenCalledTimes(1);
        expect(args.onClick).toHaveBeenCalledWith(baseNode);
    },
};
