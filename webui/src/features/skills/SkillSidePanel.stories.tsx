// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { mockCustomSkills } from "@/testing/data";

import { SkillSidePanel } from "./SkillSidePanel";

const skillWithAgents = mockCustomSkills[0];
const skillNoAgents = mockCustomSkills[1];
const skillPending = mockCustomSkills[2];
const skillFailed = mockCustomSkills[3];

const ControlledWrapper: React.FC<{
    skill: (typeof mockCustomSkills)[0];
    initialOpen?: boolean;
}> = ({ skill, initialOpen = true }) => {
    const [isOpen, setIsOpen] = useState(initialOpen);

    if (!isOpen) {
        return (
            <div className="p-4">
                <button data-testid="reopenButton" onClick={() => setIsOpen(true)}>
                    Open Panel
                </button>
                <p data-testid="panelClosedMessage">Side panel is closed</p>
            </div>
        );
    }

    return <SkillSidePanel skill={skill} onClose={() => setIsOpen(false)} />;
};

const meta = {
    title: "Skills/SkillSidePanel",
    component: SkillSidePanel,
    parameters: {
        docs: {
            description: {
                component: "Side panel displaying skill details and agent usage. Mirrors `ToolsetSidePanel` — drops the inline status section in favour of surfacing pending/failed inline on the list row.",
            },
        },
    },
    decorators: [
        Story => (
            <div className="ml-auto h-screen w-2/5 border-l">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof SkillSidePanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const WithAgents: Story = {
    args: {
        skill: skillWithAgents,
        onClose: fn(),
        onOpen: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Customer Knowledge")).toBeInTheDocument();
        expect(canvas.getByText("Skill Details")).toBeInTheDocument();
        expect(canvas.getByText("Agent Usage (2)")).toBeInTheDocument();
        expect(canvas.getByText("Support Agent")).toBeInTheDocument();
        expect(canvas.getByText("Sales Agent")).toBeInTheDocument();
        expect(canvas.queryByText("No agents are currently using this skill")).not.toBeInTheDocument();
    },
};

export const WithoutAgents: Story = {
    args: {
        skill: skillNoAgents,
        onClose: fn(),
        onOpen: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Release Notes Author")).toBeInTheDocument();
        expect(canvas.getByText("Agent Usage (0)")).toBeInTheDocument();
        expect(canvas.getByText("No agents are currently using this skill")).toBeInTheDocument();
    },
};

export const OpenCloseBehaviour: StoryObj<{ skill: typeof skillWithAgents; initialOpen: boolean }> = {
    render: args => <ControlledWrapper skill={args.skill} initialOpen={args.initialOpen} />,
    args: {
        skill: skillWithAgents,
        initialOpen: true,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Customer Knowledge")).toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("skillSidePanelCloseButton"));

        expect(await canvas.findByTestId("panelClosedMessage")).toBeInTheDocument();
        expect(canvas.queryByText("Customer Knowledge")).not.toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("reopenButton"));
        expect(await canvas.findByText("Customer Knowledge")).toBeInTheDocument();
    },
};

export const OpenSkillButton: Story = {
    args: {
        skill: skillWithAgents,
        onClose: fn(),
        onOpen: fn(),
    },
    play: async ({ args, canvasElement }) => {
        const canvas = within(canvasElement);

        const openBtn = canvas.getByTestId("openSkillButton");
        expect(openBtn).toHaveTextContent("Open Skill");

        await userEvent.click(openBtn);

        expect(args.onOpen).toHaveBeenCalledOnce();
    },
};

export const OpenButtonHiddenWhenOnOpenAbsent: Story = {
    args: {
        skill: skillWithAgents,
        onClose: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.queryByTestId("openSkillButton")).not.toBeInTheDocument();
    },
};

export const PendingStatus: Story = {
    args: {
        skill: skillPending,
        onClose: fn(),
        onOpen: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Quarterly Report")).toBeInTheDocument();
        expect(canvas.queryByText("Pending")).not.toBeInTheDocument();
        expect(canvas.queryByText("Processing")).not.toBeInTheDocument();
    },
};

export const FailedStatus: Story = {
    args: {
        skill: skillFailed,
        onClose: fn(),
        onOpen: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Legacy Import")).toBeInTheDocument();
        expect(canvas.queryByText("Discovery Failed")).not.toBeInTheDocument();
    },
};
