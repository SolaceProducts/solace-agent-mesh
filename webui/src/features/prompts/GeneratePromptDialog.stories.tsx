// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { screen, within } from "storybook/test";

import { GeneratePromptDialog } from "./GeneratePromptDialog";

const meta = {
    title: "Prompts/GeneratePromptDialog",
    component: GeneratePromptDialog,
    parameters: {
        docs: {
            description: {
                component: "The dialog for generating a prompt",
            },
        },
    },
} satisfies Meta<typeof GeneratePromptDialog>;

export default meta;
type Story = StoryObj<typeof GeneratePromptDialog>;

export const Default: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Generation will be cancelled"),
        onGenerate: () => alert("Prompt will be generated"),
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        await dialogContent.findByTestId("generatePromptButton");
    },
};

export const NoInputValidationError: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Generation will be cancelled"),
        onGenerate: () => alert("Prompt will be generated"),
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const importButton = await dialogContent.findByTestId("generatePromptButton");
        importButton.click();
    },
};
