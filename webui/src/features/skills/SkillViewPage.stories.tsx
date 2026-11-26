// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { mockCustomSkills } from "@/testing/data";
import { skillToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { SkillViewPage } from "./SkillViewPage";

const skillReady = mockCustomSkills[0];
const skillPending = mockCustomSkills[2];
const skillFailed = mockCustomSkills[3];

const filesResponse = paginatedResponse([
    { path: "references/guide.md", category: "reference", sizeBytes: 1024 },
    { path: "assets/logo.png", category: "asset", sizeBytes: 2048 },
]);

const skillDetailHandlers = (skill: (typeof mockCustomSkills)[0]) =>
    withDefaults(
        http.get(`*/api/v2/platform/skills/${skill.id}/files`, () => HttpResponse.json(filesResponse)),
        http.get(`*/api/v2/platform/skills/${skill.id}`, () => HttpResponse.json({ data: skillToV2(skill) }))
    );

const meta = {
    title: "Skills/SkillViewPage",
    component: SkillViewPage,
    parameters: {
        docs: {
            description: {
                component: "Full-page view for a single custom skill — Details / Agent Usage tabs, with Reference, Tools, and Assets sections plus a file previewer on the Details tab.",
            },
        },
    },
} satisfies Meta<typeof SkillViewPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        msw: { handlers: skillDetailHandlers(skillReady) },
        routerValues: { initialPath: `/skills/${skillReady.id}`, loader: () => ({ skillId: skillReady.id }) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect((await canvas.findAllByText("Customer Knowledge")).length).toBeGreaterThan(0);
        expect(canvas.getByRole("button", { name: /Re-upload Skill/i })).toBeInTheDocument();
        expect(canvas.getByText("Ready")).toBeInTheDocument();
        expect(canvas.getByText(/customer-support interactions/i)).toBeInTheDocument();
        expect(canvas.getByText("Reference")).toBeInTheDocument();
        expect(canvas.getByText("Tools")).toBeInTheDocument();
        expect(canvas.getByText("Assets")).toBeInTheDocument();

        const closeButtons = canvas.getAllByRole("button", { name: /^Close$/ });
        expect(closeButtons.length).toBe(1);
    },
};

export const FilePreview: Story = {
    parameters: {
        msw: {
            handlers: [http.get(`*/api/v2/platform/skills/${skillReady.id}/fileContent`, () => new HttpResponse("# Guide\n\nHello world", { headers: { "content-type": "text/markdown" } })), ...skillDetailHandlers(skillReady)],
        },
        routerValues: { initialPath: `/skills/${skillReady.id}`, loader: () => ({ skillId: skillReady.id }) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Customer Knowledge");
        await userEvent.click(await canvas.findByText("guide.md"));
        expect(await canvas.findByText("Hello world")).toBeInTheDocument();
    },
};

export const AgentUsageTab: Story = {
    parameters: {
        msw: { handlers: skillDetailHandlers(skillReady) },
        routerValues: { initialPath: `/skills/${skillReady.id}`, loader: () => ({ skillId: skillReady.id }) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Customer Knowledge");
        await userEvent.click(canvas.getByRole("tab", { name: /Agent Usage/i }));
        expect(await canvas.findByText("Support Agent")).toBeInTheDocument();
    },
};

export const PendingStatus: Story = {
    parameters: {
        msw: { handlers: skillDetailHandlers(skillPending) },
        routerValues: { initialPath: `/skills/${skillPending.id}`, loader: () => ({ skillId: skillPending.id }) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Quarterly Report");
        expect(canvas.getByText("Processing")).toBeInTheDocument();
        expect(canvas.queryByText("Pending")).not.toBeInTheDocument();
    },
};

export const FailedStatus: Story = {
    parameters: {
        msw: { handlers: skillDetailHandlers(skillFailed) },
        routerValues: { initialPath: `/skills/${skillFailed.id}`, loader: () => ({ skillId: skillFailed.id }) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Legacy Import");
        expect(canvas.getByText("Failed")).toBeInTheDocument();
        expect(canvas.queryByText("Discovery Failed")).not.toBeInTheDocument();
    },
};

export const ReuploadNavigation: Story = {
    parameters: {
        msw: { handlers: skillDetailHandlers(skillReady) },
        routerValues: { initialPath: `/skills/${skillReady.id}`, loader: () => ({ skillId: skillReady.id }) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Customer Knowledge");
        await userEvent.click(canvas.getByRole("button", { name: /Re-upload Skill/i }));
    },
};

export const Loading: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(`*/api/v2/platform/skills/${skillReady.id}/files`, () => HttpResponse.json(filesResponse)),
                http.get(`*/api/v2/platform/skills/${skillReady.id}`, async () => {
                    await delay("infinite");
                    return HttpResponse.json({ data: skillToV2(skillReady) });
                })
            ),
        },
        routerValues: { initialPath: `/skills/${skillReady.id}`, loader: () => ({ skillId: skillReady.id }) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Loading Skill...")).toBeInTheDocument();
    },
};

export const NotFound: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(http.get(`*/api/v2/platform/skills/missing-id`, () => HttpResponse.json({ message: "Not Found", errorId: "stub-not-found" }, { status: 404 }))),
        },
        routerValues: { initialPath: "/skills/missing-id", loader: () => ({ skillId: "missing-id" }) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const found = await Promise.race([canvas.findByText(/Error loading skill/), canvas.findByText("Skill not found")]);
        expect(found).toBeInTheDocument();
    },
};
