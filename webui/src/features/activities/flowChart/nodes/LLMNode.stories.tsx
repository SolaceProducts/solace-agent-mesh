// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import LLMNode from "@/features/activities/flowChart/nodes/LLMNode";

import { createActivityLayoutNode } from "@/testing/helpers/layoutNodes";

const meta = {
    title: "Activities/ActivitiesVisualization/LLMNode",
    component: LLMNode,
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
} satisfies Meta<typeof LLMNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const baseNode = createActivityLayoutNode({
    id: "llm-1",
    type: "llm",
    width: 200,
    height: 50,
    data: { label: "gpt-4o" },
});

const findClickable = (canvas: ReturnType<typeof within>) => canvas.getByRole("button");

export const Default: Story = {
    args: {
        node: baseNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("gpt-4o")).toBeInTheDocument();
        const clickable = findClickable(canvas);
        expect(clickable).toBeInTheDocument();
        expect(clickable).toHaveAttribute("tabIndex", "0");
    },
};
