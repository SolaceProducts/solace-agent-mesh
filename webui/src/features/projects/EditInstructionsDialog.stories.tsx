// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, within } from "storybook/test";

import { emptyProject, populatedProject } from "@/testing/data/projects";

import { EditInstructionsDialog } from "./EditInstructionsDialog";
import { DEFAULT_MAX_INSTRUCTIONS_LENGTH as LIMIT } from "./validation";

const meta = {
    title: "Projects/EditInstructionsDialog",
    component: EditInstructionsDialog,
    parameters: {
        docs: {
            description: {
                component: `Dialog for editing project instructions. Validates the character limit supplied by the backend via /config, falling back to ${LIMIT}.`,
            },
        },
    },
} satisfies Meta<typeof EditInstructionsDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Will close the dialog."),
        onSave: async () => {},
        project: populatedProject,
        isSaving: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();
        expect(await dialogContent.findByRole("button", { name: "Discard Changes" })).toBeEnabled();
    },
};

export const Empty: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Will close the dialog."),
        onSave: async () => {},
        project: emptyProject,
        isSaving: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        const textarea = await dialogContent.findByPlaceholderText("Add instructions for this project...");
        expect(textarea).toHaveValue("");
    },
};

export const InstructionCharacterLimitError: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Will close the dialog."),
        onSave: async () => {},
        project: emptyProject,
        isSaving: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        const textarea = await dialogContent.findByPlaceholderText("Add instructions for this project...");

        const atLimitText = "a".repeat(LIMIT);
        await userEvent.click(textarea);
        await userEvent.paste(atLimitText);
        expect(await dialogContent.findByText(`${LIMIT} / ${LIMIT}`)).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();

        await userEvent.type(textarea, "b");
        expect(await dialogContent.findByText(`Instructions must be ${LIMIT} characters or fewer (currently ${LIMIT + 1})`)).toBeInTheDocument();

        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeDisabled();
    },
};

export const OverLimitPasteIsNotTruncated: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Will close the dialog."),
        onSave: async () => {},
        project: emptyProject,
        isSaving: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const textarea = await dialogContent.findByPlaceholderText("Add instructions for this project...");

        const overLimitText = "a".repeat(LIMIT + 500);
        await userEvent.click(textarea);
        await userEvent.paste(overLimitText);

        expect(textarea).toHaveValue(overLimitText);
        expect(await dialogContent.findByText(`Instructions must be ${LIMIT} characters or fewer (currently ${LIMIT + 500})`)).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeDisabled();
        expect(textarea).toHaveAttribute("aria-invalid", "true");
        expect(await dialogContent.findByRole("alert")).toBeInTheDocument();
    },
};

export const BackendSuppliedLimit: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Will close the dialog."),
        onSave: async () => {},
        project: emptyProject,
        isSaving: false,
    },
    parameters: {
        configContext: { validationLimits: { projectInstructionsMax: 120 } },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const textarea = await dialogContent.findByPlaceholderText("Add instructions for this project...");
        await userEvent.click(textarea);
        await userEvent.paste("a".repeat(120));

        expect(await dialogContent.findByText("120 / 120")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();

        await userEvent.paste("b");
        expect(await dialogContent.findByText("Instructions must be 120 characters or fewer (currently 121)")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeDisabled();
    },
};

export const NonPositiveAdvertisedLimitFallsBack: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Will close the dialog."),
        onSave: async () => {},
        project: emptyProject,
        isSaving: false,
    },
    parameters: {
        configContext: { validationLimits: { projectInstructionsMax: 0 } },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const textarea = await dialogContent.findByPlaceholderText("Add instructions for this project...");
        await userEvent.click(textarea);
        await userEvent.paste("a normal instruction block");

        expect(await dialogContent.findByText(`26 / ${LIMIT}`)).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();
        expect(textarea).toHaveAttribute("aria-invalid", "false");
    },
};

export const TrailingWhitespaceAtLimitStaysSavable: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Will close the dialog."),
        onSave: async () => {},
        project: emptyProject,
        isSaving: false,
    },
    parameters: {
        configContext: { validationLimits: { projectInstructionsMax: 40 } },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const textarea = await dialogContent.findByPlaceholderText("Add instructions for this project...");
        await userEvent.click(textarea);
        await userEvent.paste(`${"a".repeat(40)}\n\n`);

        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();
        expect(textarea).toHaveAttribute("aria-invalid", "false");
    },
};
