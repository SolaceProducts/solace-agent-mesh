// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, within } from "storybook/test";

import { artifactWithLongDescription } from "@/testing/data/artifactInfos";

import { FileDetailsDialog } from "./FileDetailsDialog";

const meta = {
    title: "Projects/FileDetailsDialog",
    component: FileDetailsDialog,
    parameters: {
        docs: {
            description: {
                component: "Dialog showing detailed information about a project file.",
            },
        },
    },
} satisfies Meta<typeof FileDetailsDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        artifact: artifactWithLongDescription,
        onClose: () => alert("Dialog will close."),
        onEdit: () => alert("Edit description clicked."),
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByText("design-spec.pdf")).toBeInTheDocument();
        expect(await dialogContent.findByText("API reference documentation for the project endpoints including authentication, data models, and error handling patterns")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Edit Description" })).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Close" })).toBeInTheDocument();
    },
};
