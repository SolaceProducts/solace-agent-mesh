// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { screen, within } from "storybook/test";

import { PromptImportDialog } from "./PromptImportDialog";

const meta = {
    title: "Prompts/PromptImportDialog",
    component: PromptImportDialog,
    parameters: {
        docs: {
            description: {
                component: "The dialog for importing a prompt",
            },
        },
    },
} satisfies Meta<typeof PromptImportDialog>;

export default meta;
type Story = StoryObj<typeof PromptImportDialog>;

export const Default: Story = {
    args: {
        open: true,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        await dialogContent.findByTestId("dialogConfirmButton");
    },
};
