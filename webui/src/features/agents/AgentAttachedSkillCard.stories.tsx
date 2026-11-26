// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { AgentAttachedSkillCard } from "./AgentAbilityCard";

const LONG_DESCRIPTION =
    "A very long skill description that should be truncated by the card layout so that it cannot push the surrounding agent view grid column past its allotted width and cause the entire page to scroll horizontally, which was a real bug we just fixed.";

const meta = {
    title: "Agents/AgentAttachedSkillCard",
    component: AgentAttachedSkillCard,
    parameters: {
        layout: "centered",
        docs: {
            description: {
                component: "Card used by the agent view's Skills section for skills attached to the agent. Mirrors the structure of `AgentToolsetCard` and `AgentConnectorCard`; the BookOpen icon matches the section header.",
            },
        },
    },
    decorators: [
        Story => (
            <div style={{ width: 600 }}>
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof AgentAttachedSkillCard>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        skill: {
            name: "Customer Knowledge",
            description: "Domain knowledge bundle for customer-support interactions.",
        },
        className: "w-full",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Customer Knowledge")).toBeInTheDocument();
        expect(canvas.getByText("Domain knowledge bundle for customer-support interactions.")).toBeInTheDocument();
    },
};

export const NoDescription: Story = {
    args: {
        skill: { name: "orphan-skill-ref" },
        className: "w-full",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("orphan-skill-ref")).toBeInTheDocument();
    },
};

export const LongDescriptionTruncates: Story = {
    args: {
        skill: { name: "verbose-skill", description: LONG_DESCRIPTION },
        className: "w-full",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const desc = canvas.getByText(LONG_DESCRIPTION);
        expect(desc).toHaveClass("truncate");
        const parentWidth = desc.parentElement!.getBoundingClientRect().width;
        expect(desc.getBoundingClientRect().width).toBeLessThanOrEqual(parentWidth);
    },
};
