// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { ExternalLink } from "lucide-react";
import { Link } from "react-router-dom";
import { expect, userEvent, within } from "storybook/test";

import { Button } from "@/ui/button";

import { DOC_LINKS } from "@/utils";

import { LearningBanner } from "./LearningBanner";

const resetStorage = (key: string) => {
    if (typeof window !== "undefined") window.localStorage.removeItem(key);
};

const meta = {
    title: "Common/LearningBanner",
    component: LearningBanner,
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
            description: {
                component:
                    "Dismissible right-rail learning card. Used to surface concept guidance, CLI workflows, or onboarding hints next to the primary form. Dismissal persists via `useLocalStorage` keyed by `storageKey`, so each placement needs a unique key.",
            },
        },
    },
} satisfies Meta<typeof LearningBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        title: "Extend Agent with Custom Toolsets",
        storageKey: "learningbanner-story-default",
        children: (
            <>
                Add custom toolsets to extend your agent's capabilities.
                <br />
                <br />
                Create custom toolsets in your development environment and upload them to the Agent Mesh.
                <br />
                <br />
                <Link to={DOC_LINKS.buildingTools} target="_blank" rel="noopener noreferrer">
                    <Button variant="link" className="p-0 has-[>svg]:px-0" title="Learn about Custom Tools">
                        <div className="truncate">Learn about Custom Tools</div>
                        <ExternalLink />
                    </Button>
                </Link>
            </>
        ),
    },
    beforeEach: () => resetStorage("learningbanner-story-default"),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Extend Agent with Custom Toolsets")).toBeInTheDocument();
    },
};

export const WithoutIcon: Story = {
    args: {
        title: "Compact info card",
        storageKey: "learningbanner-story-no-icon",
        showIcon: false,
        children: <>Slimmer variant — drops the leading icon for a tighter layout when the surrounding context already conveys meaning.</>,
    },
    beforeEach: () => resetStorage("learningbanner-story-no-icon"),
};

export const Dismissed: Story = {
    args: {
        title: "Hidden when dismissed",
        storageKey: "learningbanner-story-dismissed",
        children: <>Clicking the dismiss button persists to localStorage; subsequent renders return null.</>,
    },
    beforeEach: () => {
        if (typeof window !== "undefined") window.localStorage.setItem("learningbanner-story-dismissed", "true");
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.queryByText("Hidden when dismissed")).not.toBeInTheDocument();
    },
};

export const Dismissible: Story = {
    args: {
        title: "Click X to dismiss",
        storageKey: "learningbanner-story-dismissible",
        children: <>Behaviour test: clicking the dismiss button removes the banner from the DOM.</>,
    },
    beforeEach: () => resetStorage("learningbanner-story-dismissible"),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const banner = canvas.getByText("Click X to dismiss");
        expect(banner).toBeInTheDocument();
        await userEvent.click(canvas.getByRole("button", { name: /dismiss/i }));
        expect(canvas.queryByText("Click X to dismiss")).not.toBeInTheDocument();
    },
};
