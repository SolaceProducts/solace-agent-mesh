// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, within } from "storybook/test";

import { ModelDeleteDialog } from "./ModelDeleteDialog";

const meta = {
    title: "Models/ModelDeleteDialog",
    component: ModelDeleteDialog,
    parameters: {
        docs: {
            description: {
                component: "Confirmation dialog for deleting a model configuration. Default models (general, planning) show a read-only message. Custom models require typing the model's alias to confirm.",
            },
        },
    },
} satisfies Meta<typeof ModelDeleteDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        onConfirm: async () => {},
        modelId: "model-123",
        modelAlias: "my-gpt-4",
        isLoading: false,
    },
    play: async ({ args }) => {
        const dialog = await screen.findByRole("dialog");
        const content = within(dialog);

        expect(content.getByText("Delete Model")).toBeInTheDocument();
        expect(await content.findByText(/code-based agents/)).toBeInTheDocument();
        expect(content.getByText(args.modelAlias)).toBeInTheDocument();

        const deleteButton = content.getByRole("button", { name: "Delete" });
        expect(deleteButton).toBeDisabled();

        const cancelButton = content.getByRole("button", { name: "Cancel" });
        expect(cancelButton).toBeEnabled();
    },
};

export const ConfirmEnabled: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        onConfirm: async () => {},
        modelId: "model-456",
        modelAlias: "my-gpt-4",
        isLoading: false,
    },
    play: async ({ args }) => {
        const dialog = await screen.findByRole("dialog");
        const content = within(dialog);

        const input = await content.findByRole("textbox");
        await userEvent.type(input, args.modelAlias);

        const deleteButton = content.getByRole("button", { name: "Delete" });
        expect(deleteButton).toBeEnabled();
    },
};

export const DefaultModelGeneral: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        onConfirm: async () => {},
        modelId: "general-model-id",
        modelAlias: "general",
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const content = within(dialog);

        expect(await content.findByText("Unable to Delete")).toBeInTheDocument();
        expect(content.getByText(/cannot be deleted/)).toBeInTheDocument();
        expect(content.getByRole("button", { name: "Close" })).toBeInTheDocument();

        expect(content.queryByRole("textbox")).not.toBeInTheDocument();
        expect(content.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
    },
};
