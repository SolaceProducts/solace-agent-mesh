// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, within } from "storybook/test";

import { jsonArtifact, pdfArtifact } from "@/testing/data/artifactInfos";

import { EditFileDescriptionDialog } from "./EditFileDescriptionDialog";

const meta = {
    title: "Projects/EditFileDescriptionDialog",
    component: EditFileDescriptionDialog,
    parameters: {
        docs: {
            description: {
                component: "Dialog for editing the description of an uploaded project file.",
            },
        },
    },
} satisfies Meta<typeof EditFileDescriptionDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        artifact: pdfArtifact,
        onClose: () => alert("Dialog will close."),
        onSave: async () => {},
        isSaving: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);
        expect(await dialogContent.findByText("api-documentation.pdf")).toBeInTheDocument();
        expect(await dialogContent.findByText("API reference documentation")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Discard Changes" })).toBeInTheDocument();
    },
};

export const DescriptionOverLimit: Story = {
    args: {
        isOpen: true,
        artifact: jsonArtifact,
        onClose: () => {},
        onSave: async () => {},
        isSaving: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const descriptionBox = await dialogContent.findByRole("textbox");
        await userEvent.click(descriptionBox);
        await userEvent.paste("a".repeat(1001));

        expect(await dialogContent.findByText(/exceeds the maximum of/i)).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeDisabled();
    },
};

export const Empty: Story = {
    args: {
        isOpen: true,
        artifact: jsonArtifact,
        onClose: () => alert("Dialog will close."),
        onSave: async () => {},
        isSaving: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByText("package.json")).toBeInTheDocument();

        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();
        expect(await dialogContent.findByRole("button", { name: "Discard Changes" })).toBeEnabled();
    },
};
