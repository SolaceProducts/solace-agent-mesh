// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import { createCustomSkills, mockBuiltinSkills, mockCustomSkills } from "@/testing/data";
import { skillToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta, paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Skill } from "@/types";

import { SkillsView } from "./SkillsView";

const createHandlers = (customList: Skill[] = mockCustomSkills, builtinList: Skill[] = mockBuiltinSkills) =>
    withDefaults(
        http.get("*/api/v2/platform/skills", ({ request }) => {
            const type = new URL(request.url).searchParams.get("origin");
            const list = type === "builtin" ? builtinList : customList;
            return HttpResponse.json(paginatedResponse(list.map(skillToV2)));
        })
    );

const meta = {
    title: "Skills/SkillsView",
    component: SkillsView,
    parameters: {
        docs: {
            description: {
                component: "List view for skills — table with search, sort, pagination, side panel on row click, and URL-driven type/status filters. Mirrors the toolsets list shape.",
            },
        },
        msw: { handlers: createHandlers() },
    },
} satisfies Meta<typeof SkillsView>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        for (const skill of mockCustomSkills) {
            expect(await canvas.findByText(skill.name)).toBeInTheDocument();
        }

        expect(canvas.getByRole("columnheader", { name: /Name/i })).toBeInTheDocument();
        expect(canvas.getByRole("columnheader", { name: /Type/i })).toBeInTheDocument();
        expect(canvas.getByRole("columnheader", { name: /Agent Usage/i })).toBeInTheDocument();
        expect(canvas.getByRole("columnheader", { name: /Created By/i })).toBeInTheDocument();
    },
};

export const TypeColumn: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("sam-knowledge");
        await canvas.findByText("Customer Knowledge");

        const builtinCell = canvas.getByTestId("skillTypeCell-sam-knowledge");
        expect(builtinCell.textContent).toBe("Built-in");

        const customCell = canvas.getByTestId("skillTypeCell-11111111-1111-1111-1111-000000000001");
        expect(customCell.textContent).toBe("Custom");
    },
};

export const Empty: Story = {
    parameters: {
        msw: { handlers: createHandlers([], []) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Upload skills to extend your agents")).toBeInTheDocument();
    },
};

export const Loading: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/skills", async () => {
                    await delay("infinite");
                    return HttpResponse.json({ data: [], meta: getPaginatedMeta(0) });
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Loading Skills...")).toBeInTheDocument();
    },
};

export const Error: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/skills", () => HttpResponse.json({ message: "Failed to connect to platform service", errorId: "stub" }, { status: 500 }))),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText(/Error loading skills/)).toBeInTheDocument();
    },
};

export const SearchFiltering: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Customer Knowledge");

        const searchInput = canvas.getByTestId("skillsSearchInput");
        await userEvent.type(searchInput, "customer");

        expect(await canvas.findByText("Customer Knowledge")).toBeInTheDocument();
        expect(canvas.queryByText("Release Notes Author")).not.toBeInTheDocument();
    },
};

export const SearchNoResults: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Customer Knowledge");

        const searchInput = canvas.getByTestId("skillsSearchInput");
        await userEvent.type(searchInput, "nonexistent-xyz");

        expect(await canvas.findByText("No skills match your filters.")).toBeInTheDocument();
    },
};

export const SidePanelOpen: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const row = await canvas.findByTestId("skillRow-11111111-1111-1111-1111-000000000001");
        await userEvent.click(row);

        const panel = await screen.findByText("Skill Details");
        expect(panel).toBeInTheDocument();
    },
};

export const FilteredByCustom: Story = {
    parameters: {
        routerValues: { initialPath: "/?type=custom" },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Customer Knowledge");

        const typeFilter = canvas.getByTestId("skillsTypeFilter");
        expect(typeFilter.textContent).toContain("Custom");

        expect(canvas.queryByText("sam-knowledge")).not.toBeInTheDocument();
    },
};

export const Pagination: Story = {
    parameters: {
        msw: { handlers: createHandlers(createCustomSkills(25), []) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Skill_00")).toBeInTheDocument();
        expect(canvas.queryByText("Skill_24")).not.toBeInTheDocument();

        while (!canvas.queryByText("Skill_24")) {
            await userEvent.click(canvas.getByLabelText("Go to next page"));
        }

        expect(await canvas.findByText("Skill_24")).toBeInTheDocument();
        expect(canvas.queryByText("Skill_00")).not.toBeInTheDocument();
    },
};
