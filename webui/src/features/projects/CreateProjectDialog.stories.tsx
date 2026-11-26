// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent, within } from "storybook/test";

import { CreateProjectDialog } from "./CreateProjectDialog";

const meta = {
    title: "Projects/CreateProjectDialog",
    component: CreateProjectDialog,
    parameters: {
        docs: {
            description: {
                component: "Dialog for creating a new project. Asks for project name (required) and description (optional; max 1000 characters).",
            },
        },
    },
} satisfies Meta<typeof CreateProjectDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Project creation will cancel. Dialog will close."),
        onSubmit: async () => {},
        isSubmitting: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByRole("button", { name: "Create Project" })).toBeInTheDocument();

        const nameLabel = await dialogContent.findByText("Project Name", { exact: false });
        expect(within(nameLabel).getByText("*")).toHaveClass("text-(--brand-wMain)");
        expect(await dialogContent.findByPlaceholderText("Project name")).toBeInTheDocument();
        expect(await dialogContent.findByPlaceholderText("Project description")).toBeInTheDocument();
    },
};

export const Empty: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Project creation will cancel. Dialog will close."),
        onSubmit: async () => {},
        isSubmitting: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);
        const submitButton = await dialogContent.findByRole("button", { name: "Create Project" });

        await userEvent.click(submitButton);
        expect(await dialogContent.findByText("Project name is required")).toBeInTheDocument();
    },
};

export const SubmitsTrimmedValues: Story = {
    args: {
        isOpen: true,
        onClose: () => {},
        onSubmit: fn(),
        isSubmitting: false,
    },
    play: async ({ args }) => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        await userEvent.type(await dialogContent.findByPlaceholderText("Project name"), "  My Project  ");
        await userEvent.type(await dialogContent.findByPlaceholderText("Project description"), "  A description  ");
        await userEvent.click(await dialogContent.findByRole("button", { name: "Create Project" }));

        expect(args.onSubmit).toHaveBeenCalledOnce();
        expect(args.onSubmit).toHaveBeenCalledWith({ name: "My Project", description: "A description" });
    },
};

export const DescriptionCharacterLimitError: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Project creation will cancel. Dialog will close."),
        onSubmit: async () => {},
        isSubmitting: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        const descriptionInput = await dialogContent.findByLabelText("Description");
        const atLimitText = "a".repeat(1000);

        await userEvent.click(descriptionInput);
        await userEvent.paste(atLimitText);
        expect(await dialogContent.findByText("1000 / 1000")).toBeInTheDocument();

        await userEvent.type(descriptionInput, "b");
        expect(await dialogContent.findByText("Description must be 1000 characters or fewer (currently 1001)")).toBeInTheDocument();
        expect(descriptionInput).toHaveAttribute("aria-invalid", "true");
        const counter = await dialogContent.findByRole("status");
        expect(counter.tagName).toBe("OUTPUT");
        expect(counter).toHaveTextContent("Description must be 1000 characters or fewer (currently 1001)");

        expect(await dialogContent.findByRole("button", { name: "Create Project" })).toBeDisabled();
    },
};

export const OverLimitDescriptionPasteIsNotTruncated: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Project creation will cancel. Dialog will close."),
        onSubmit: async () => {},
        isSubmitting: false,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const descriptionInput = await dialogContent.findByLabelText("Description");
        const overLimitText = "a".repeat(1200);

        await userEvent.click(descriptionInput);
        await userEvent.paste(overLimitText);

        expect(descriptionInput).toHaveValue(overLimitText);
        expect(await dialogContent.findByText("Description must be 1000 characters or fewer (currently 1200)")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Create Project" })).toBeDisabled();
    },
};

export const DescriptionLimitFromBackend: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Project creation will cancel. Dialog will close."),
        onSubmit: async () => {},
        isSubmitting: false,
    },
    parameters: {
        configContext: { validationLimits: { projectDescriptionMax: 60 } },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const descriptionInput = await dialogContent.findByLabelText("Description");
        await userEvent.click(descriptionInput);
        await userEvent.paste("a".repeat(60));

        expect(await dialogContent.findByText("60 / 60")).toBeInTheDocument();

        await userEvent.paste("b");
        expect(await dialogContent.findByText("Description must be 60 characters or fewer (currently 61)")).toBeInTheDocument();
    },
};

export const DescriptionTrailingWhitespaceStaysSubmittable: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Project creation will cancel. Dialog will close."),
        onSubmit: async () => {},
        isSubmitting: false,
    },
    parameters: {
        configContext: { validationLimits: { projectDescriptionMax: 30 } },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        await userEvent.type(await dialogContent.findByPlaceholderText("Project name"), "Whitespace Project");

        const descriptionInput = await dialogContent.findByLabelText("Description");
        await userEvent.click(descriptionInput);
        await userEvent.paste(`${"a".repeat(30)}\n\n`);

        expect(descriptionInput).toHaveAttribute("aria-invalid", "false");
        expect(await dialogContent.findByText("30 / 30")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Create Project" })).toBeEnabled();
    },
};

export const NonPositiveAdvertisedLimitFallsBack: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Project creation will cancel. Dialog will close."),
        onSubmit: async () => {},
        isSubmitting: false,
    },
    parameters: {
        configContext: { validationLimits: { projectDescriptionMax: 0 } },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        await userEvent.type(await dialogContent.findByPlaceholderText("Project name"), "Zero Limit Project");

        const descriptionInput = await dialogContent.findByLabelText("Description");
        await userEvent.click(descriptionInput);
        await userEvent.paste("a normal description");

        expect(await dialogContent.findByText("20 / 1000")).toBeInTheDocument();
        expect(descriptionInput).toHaveAttribute("aria-invalid", "false");
        expect(await dialogContent.findByRole("button", { name: "Create Project" })).toBeEnabled();
    },
};

export const DescriptionCountsEmojiAsOneCharacter: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Project creation will cancel. Dialog will close."),
        onSubmit: async () => {},
        isSubmitting: false,
    },
    parameters: {
        configContext: { validationLimits: { projectDescriptionMax: 10 } },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        await userEvent.type(await dialogContent.findByPlaceholderText("Project name"), "Emoji Project");

        const descriptionInput = await dialogContent.findByLabelText("Description");
        await userEvent.click(descriptionInput);
        await userEvent.paste("😀".repeat(8));

        expect(await dialogContent.findByText("8 / 10")).toBeInTheDocument();
        expect(descriptionInput).toHaveAttribute("aria-invalid", "false");
        expect(await dialogContent.findByRole("button", { name: "Create Project" })).toBeEnabled();
    },
};

export const Submitting: Story = {
    args: {
        isOpen: true,
        onClose: () => alert("Bad. cannot close the dialog while submitting."),
        onSubmit: async () => {},
        isSubmitting: true,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        const submitButton = await dialogContent.findByRole("button", { name: "Create Project" });
        const cancelButton = await dialogContent.findByRole("button", { name: "Cancel" });

        expect(submitButton).toBeDisabled();
        expect(cancelButton).toBeDisabled();
    },
};
