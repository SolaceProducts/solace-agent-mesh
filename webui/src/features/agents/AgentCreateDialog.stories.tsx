// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fireEvent, screen, within } from "storybook/test";

import { SAFE_NAME_DESCRIPTION } from "@/common/form";

import { AgentCreateDialog } from "./AgentCreateDialog";

const meta = {
    title: "Agents/AgentCreateDialog",
    component: AgentCreateDialog,
    parameters: {
        docs: {
            description: {
                component: "Dialog for creating a new agent with name and description fields.",
            },
        },
    },
} satisfies Meta<typeof AgentCreateDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        await within(dialog).findByText("Create Agent");
        await within(dialog).findByText("Name");
        await within(dialog).findByText("Description");
        await within(dialog).findByRole("button", { name: /Continue/i });
        await within(dialog).findByRole("button", { name: /Cancel/i });
    },
};

export const NameTooShort: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const nameInput = within(dialog).getByTestId("agentNameInput");
        const descriptionInput = within(dialog).getByTestId("agentDescriptionInput");

        fireEvent.change(nameInput, { target: { value: "AB" } });
        fireEvent.change(descriptionInput, { target: { value: "This is a valid description for the agent" } });

        await within(dialog)
            .findByRole("button", { name: /Continue/i })
            .then(button => button.click());

        await within(dialog).findByText("Name must be at least 3 characters");
    },
};

export const NameTooLong: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const nameInput = within(dialog).getByTestId("agentNameInput");
        const descriptionInput = within(dialog).getByTestId("agentDescriptionInput");

        fireEvent.change(nameInput, { target: { value: "A".repeat(41) } });
        fireEvent.change(descriptionInput, { target: { value: "This is a valid description for the agent" } });

        await within(dialog)
            .findByRole("button", { name: /Continue/i })
            .then(button => button.click());

        await within(dialog).findByText("Name must be 40 characters or less");
    },
};

export const NameWithControlCharacters: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const nameInput = within(dialog).getByTestId("agentNameInput");
        const descriptionInput = within(dialog).getByTestId("agentDescriptionInput");

        fireEvent.change(nameInput, { target: { value: "\x1B[31mRed Text\x1B[0m" } });
        fireEvent.change(descriptionInput, { target: { value: "This is a valid description for the agent" } });

        await within(dialog)
            .findByRole("button", { name: /Continue/i })
            .then(button => button.click());

        await within(dialog).findByText(SAFE_NAME_DESCRIPTION);
    },
};

export const DescriptionTooShort: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const nameInput = within(dialog).getByTestId("agentNameInput");
        const descriptionInput = within(dialog).getByTestId("agentDescriptionInput");

        fireEvent.change(nameInput, { target: { value: "Valid Name" } });
        fireEvent.change(descriptionInput, { target: { value: "Short" } });

        await within(dialog)
            .findByRole("button", { name: /Continue/i })
            .then(button => button.click());

        await within(dialog).findByText("Value must be at least 10 characters");
    },
};

export const DescriptionTooLong: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const nameInput = within(dialog).getByTestId("agentNameInput");
        const descriptionInput = within(dialog).getByTestId("agentDescriptionInput");

        fireEvent.change(nameInput, { target: { value: "Valid Name" } });
        fireEvent.change(descriptionInput, { target: { value: "A".repeat(1001) } });

        await within(dialog)
            .findByRole("button", { name: /Continue/i })
            .then(button => button.click());

        await within(dialog).findByText("Value must be 1000 characters or less");
    },
};

export const MultipleValidationErrors: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const nameInput = within(dialog).getByTestId("agentNameInput");
        const descriptionInput = within(dialog).getByTestId("agentDescriptionInput");

        fireEvent.change(nameInput, { target: { value: "AB" } });
        fireEvent.change(descriptionInput, { target: { value: "Short" } });

        await within(dialog)
            .findByRole("button", { name: /Continue/i })
            .then(button => button.click());

        await within(dialog).findByText("Name must be at least 3 characters");
        await within(dialog).findByText("Value must be at least 10 characters");
    },
};

export const CharacterCount: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const descriptionInput = within(dialog).getByTestId("agentDescriptionInput");

        fireEvent.change(descriptionInput, { target: { value: "This is a test description" } });

        await within(dialog).findByText("26/1000");
    },
};

export const ValidForm: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const nameInput = within(dialog).getByTestId("agentNameInput");
        const descriptionInput = within(dialog).getByTestId("agentDescriptionInput");

        fireEvent.change(nameInput, { target: { value: "My Test Agent" } });
        fireEvent.change(descriptionInput, { target: { value: "This is a valid description for the test agent that meets the minimum length requirement." } });

        const continueButton = await within(dialog).findByRole("button", { name: /Continue/i });
        expect(continueButton).not.toBeDisabled();
    },
};
