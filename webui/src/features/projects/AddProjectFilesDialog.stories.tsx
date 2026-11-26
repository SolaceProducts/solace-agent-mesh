// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, within } from "storybook/test";

import { createMockFile, createMockFileList } from "@/testing/helpers/files";

import { AddProjectFilesDialog } from "./AddProjectFilesDialog";

const meta = {
    title: "Projects/AddProjectFilesDialog",
    component: AddProjectFilesDialog,
    parameters: {
        docs: {
            description: {
                component: "Dialog for adding files to a project. Allows users to add descriptions for each uploaded file.",
            },
        },
    },
} satisfies Meta<typeof AddProjectFilesDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        files: createMockFileList([createMockFile("api-documentation.pdf", 524288, "application/pdf")]),
        onClose: () => alert("Will close the dialog."),
        onConfirm: async () => {},
        isSubmitting: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        const descriptionBox = await dialogContent.findByRole("textbox");
        await userEvent.type(descriptionBox, "API documentation for the project.");

        expect(await dialogContent.findByText("api-documentation.pdf")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Upload 1 File(s)" })).toBeInTheDocument();
    },
};

export const MultipleFiles: Story = {
    args: {
        isOpen: true,
        files: createMockFileList([createMockFile("api-documentation.pdf", 524288, "application/pdf"), createMockFile("architecture-diagram.png", 204800, "image/png"), createMockFile("package.json", 1024, "application/json")]),
        onClose: () => alert("Will close the dialog."),
        onConfirm: async () => {},
        isSubmitting: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByText("api-documentation.pdf")).toBeInTheDocument();
        expect(await dialogContent.findByText("architecture-diagram.png")).toBeInTheDocument();
        expect(await dialogContent.findByText("package.json")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Upload 3 File(s)" })).toBeEnabled();
    },
};

export const DescriptionOverLimit: Story = {
    args: {
        isOpen: true,
        files: createMockFileList([createMockFile("api-documentation.pdf", 524288, "application/pdf")]),
        onClose: () => {},
        onConfirm: async () => {},
        isSubmitting: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const descriptionBox = await dialogContent.findByRole("textbox");
        await userEvent.click(descriptionBox);
        await userEvent.paste("a".repeat(1001));

        expect(await dialogContent.findByText(/exceeds the maximum of/i)).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Upload 1 File(s)" })).toBeDisabled();
    },
};

export const Loading: Story = {
    args: {
        isOpen: true,
        files: createMockFileList([createMockFile("api-documentation.pdf", 524288, "application/pdf")]),
        onClose: () => alert("Will close the dialog."),
        onConfirm: async () => {},
        isSubmitting: true,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        const descriptionBox = await dialogContent.findByRole("textbox");
        expect(descriptionBox).toBeDisabled();

        expect(await dialogContent.findByRole("button", { name: "Upload 1 File(s)" })).toBeDisabled();
        expect(await dialogContent.findByRole("button", { name: "Cancel" })).toBeDisabled();
    },
};
