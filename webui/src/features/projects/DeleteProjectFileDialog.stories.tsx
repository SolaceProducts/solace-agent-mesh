// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, within } from "storybook/test";

import { pdfArtifact } from "@/testing/data/artifactInfos";

import { DeleteProjectFileDialog } from "./DeleteProjectFileDialog";

const meta = {
    title: "Projects/DeleteProjectFileDialog",
    component: DeleteProjectFileDialog,
    parameters: {
        docs: {
            description: {
                component: "Confirmation dialog for deleting a project file.",
            },
        },
    },
} satisfies Meta<typeof DeleteProjectFileDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        fileToDelete: pdfArtifact,
        setFileToDelete: () => alert("File deletion cancelled."),
        handleConfirmDelete: () => alert("File will be deleted."),
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByText("api-documentation.pdf")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Delete" })).toBeInTheDocument();
    },
};
