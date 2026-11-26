// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import JSZip from "jszip";
import { http, HttpResponse } from "msw";
import { expect, userEvent, waitFor, within } from "storybook/test";

import { mockCustomSkills } from "@/testing/data";
import { skillToV2 } from "@/testing/data/platformWireV2";
import { withDefaults } from "@/testing/helpers";

import { SkillUploadPage } from "./SkillUploadPage";

async function makeSkillZip(skillName: string): Promise<File> {
    const zip = new JSZip();
    zip.file("SKILL.md", `---\nname: ${skillName}\ndescription: A test skill.\n---\n# body\n`);
    const blob = await zip.generateAsync({ type: "blob" });
    return new File([blob], `${skillName}.zip`, { type: "application/zip" });
}

const pdfBytes = new Uint8Array([0x25, 0x50, 0x44, 0x46, 0x2d, 0x31, 0x2e, 0x34]);

const meta = {
    title: "Skills/SkillUploadPage",
    component: SkillUploadPage,
    parameters: {
        docs: {
            description: {
                component: "Skill upload page. Name is derived from the SKILL.md frontmatter inside the uploaded ZIP — no name input is shown to the user.",
            },
        },
        routerValues: { initialPath: "/skills/upload" },
    },
} satisfies Meta<typeof SkillUploadPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.queryByTestId("skillNameInput")).not.toBeInTheDocument();

        const descInput = (await canvas.findByTestId("skillDescriptionInput")) as HTMLTextAreaElement;
        expect(descInput.tagName).toBe("TEXTAREA");
        expect(descInput.placeholder).toBe("");
        expect(descInput.maxLength).toBe(1000);
        expect(canvas.getByText(/explain what your skill does/i)).toBeInTheDocument();
        expect(canvas.getByText("0/1000")).toBeInTheDocument();

        expect(canvas.getByText(/SKILL\.md frontmatter/i)).toBeInTheDocument();
    },
};

export const CharacterCounterUpdates: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const descInput = (await canvas.findByTestId("skillDescriptionInput")) as HTMLTextAreaElement;
        await userEvent.type(descInput, "Hello world");
        expect(canvas.getByText("11/1000")).toBeInTheDocument();
    },
};

export const ZipMagicByteRejectsRenamedFile: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const fileInput = (await canvas.findByTestId("skillFileUpload")) as HTMLInputElement;
        const fakeZip = new File([pdfBytes], "doc.zip", { type: "application/zip" });
        await userEvent.upload(fileInput, fakeZip);

        expect(await canvas.findByText(/not a valid ZIP archive/i)).toBeInTheDocument();
    },
};

export const ZipMagicByteAcceptsRealZip: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const fileInput = (await canvas.findByTestId("skillFileUpload")) as HTMLInputElement;
        await userEvent.upload(fileInput, await makeSkillZip("magic-ok-skill"));

        expect(canvas.queryByText(/not a valid ZIP archive/i)).not.toBeInTheDocument();
        expect(canvas.queryByText(/must be a \.zip archive/i)).not.toBeInTheDocument();
    },
};

export const ReuploadMode: Story = {
    parameters: {
        routerValues: {
            initialPath: "/skills/11111111-1111-1111-1111-000000000001/upload",
            loader: () => ({ skillId: "11111111-1111-1111-1111-000000000001" }),
        },
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/skills/:id", ({ params }) => {
                    const skill = mockCustomSkills.find(s => s.id === params.id);
                    return skill ? HttpResponse.json({ data: skillToV2(skill) }) : HttpResponse.json({ message: "Not found", errorId: "stub" }, { status: 404 });
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("skillFileUpload");

        expect(canvas.queryByTestId("skillNameInput")).not.toBeInTheDocument();
        expect(canvas.queryByTestId("skillDescriptionInput")).not.toBeInTheDocument();
        expect(canvas.getByTestId("uploadSkillButton")).toHaveTextContent("Re-upload Skill");
    },
};

export const PostOnCreate: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(http.post("*/api/v2/platform/skills", () => HttpResponse.json({ data: skillToV2({ ...mockCustomSkills[0], id: "new-skill", name: "fresh-skill" }) }))),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.type(await canvas.findByTestId("skillDescriptionInput"), "A brand new skill.");
        const fileInput = canvas.getByTestId("skillFileUpload") as HTMLInputElement;
        await userEvent.upload(fileInput, await makeSkillZip("fresh-skill"));
        await canvas.findByText("fresh-skill.zip");

        await userEvent.click(canvas.getByTestId("uploadSkillButton"));

        await waitFor(() => {
            expect(canvas.queryByText(/upload failed/i)).not.toBeInTheDocument();
        });
    },
};

export const PostOnCreateError: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(http.post("*/api/v2/platform/skills", () => HttpResponse.json({ message: "Server exploded", errorId: "stub" }, { status: 500 }))),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.type(await canvas.findByTestId("skillDescriptionInput"), "A brand new skill.");
        const fileInput = canvas.getByTestId("skillFileUpload") as HTMLInputElement;
        await userEvent.upload(fileInput, await makeSkillZip("fresh-skill"));
        await canvas.findByText("fresh-skill.zip");

        await userEvent.click(canvas.getByTestId("uploadSkillButton"));

        const banner = await canvas.findByRole("alert");
        expect(banner).toHaveTextContent(/server exploded/i);
    },
};
