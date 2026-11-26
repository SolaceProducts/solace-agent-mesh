// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { SkillsPage } from "@/features/skills/SkillsPage";

import { mockBuiltinSkills, mockCustomSkills } from "@/testing/data";
import { skillToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const skillListHandlers = withDefaults(
    http.get("*/api/v2/platform/skills", ({ request }) => {
        const type = new URL(request.url).searchParams.get("origin");
        return HttpResponse.json(paginatedResponse((type === "builtin" ? mockBuiltinSkills : mockCustomSkills).map(skillToV2)));
    })
);

const openRowMenu = async (canvas: ReturnType<typeof within>, skillId: string) => {
    const row = await canvas.findByTestId(`skillRow-${skillId}`);
    await userEvent.click(within(row).getByTestId("skillMorePopoverTrigger"));
    return screen.findByRole("dialog");
};

const meta = {
    title: "Skills/SkillsPage/Reduced Access",
    component: SkillsPage,
    parameters: {
        docs: {
            description: {
                component: "The skills list as seen by a skill:*:read holder — the page is reachable, its write affordances are not.",
            },
        },
    },
} satisfies Meta<typeof SkillsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CanRead: Story = {
    parameters: {
        msw: { handlers: skillListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readSkills,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Skills")).toBeInTheDocument();
        const row = await canvas.findByTestId(`skillRow-${mockCustomSkills[0].id}`);

        expect(canvas.queryByText("Create Skill")).not.toBeInTheDocument();
        expect(canvas.getByTitle("Refresh Skills")).toBeInTheDocument();
        expect(within(row).queryByTestId("skillMorePopoverTrigger")).not.toBeInTheDocument();
    },
};

export const CanCreate: Story = {
    parameters: {
        msw: { handlers: skillListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readSkills || capability === CAPABILITIES.createSkills,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Create Skill")).toBeInTheDocument();
    },
};

export const CanUpdateAndDelete: Story = {
    parameters: {
        msw: { handlers: skillListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: () => true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openRowMenu(canvas, mockCustomSkills[0].id);
        expect(within(menu).getByText("Re-upload")).toBeInTheDocument();
        expect(within(menu).getByText("Delete")).toBeInTheDocument();
    },
};
