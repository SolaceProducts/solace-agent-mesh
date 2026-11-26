// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta } from "@storybook/react-vite";
import { expect, screen, userEvent, within } from "storybook/test";

import { Button } from "@/ui/button";

import { ErrorDialog } from "./ErrorDialog";

const meta = {
    title: "Common/ErrorDialog",
    component: ErrorDialog,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "An error dialog component for displaying error messages with optional details",
            },
        },
    },
} satisfies Meta<typeof ErrorDialog>;

export default meta;

function DefaultStory() {
    const [open, setOpen] = useState(true);
    return <ErrorDialog title="Default Error" error="Something went wrong" open={open} onOpenChange={setOpen} />;
}

function WithErrorDetailsStory() {
    const [open, setOpen] = useState(true);
    return <ErrorDialog title="Detailed Error" error="Something went wrong" errorDetails="This action is forbidden. Ensure you have the right authorization." open={open} onOpenChange={setOpen} />;
}

function WithButtonStory() {
    const [open, setOpen] = useState(false);
    return (
        <>
            <Button onClick={() => setOpen(true)} className="w-50">
                Show Error Dialog
            </Button>
            <ErrorDialog
                title="Long Detailed Error"
                error="Something went wrong"
                errorDetails="This action is cannot be performed due to some very long reason that needs to wrap in this dialog for demonstration purposes."
                open={open}
                onOpenChange={setOpen}
            />
        </>
    );
}

export const Default = {
    render: () => <DefaultStory />,
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await expect(dialog).toBeInTheDocument();

        const dialogContent = within(dialog);
        await dialogContent.findByText("Default Error");
        await dialogContent.findByText("Something went wrong");
        await dialogContent.findByRole("button", { name: "Close" });
    },
};

export const WithErrorDetails = {
    render: () => <WithErrorDetailsStory />,
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await expect(dialog).toBeInTheDocument();

        const dialogContent = within(dialog);
        await dialogContent.findByText("Detailed Error");
        await dialogContent.findByText("Something went wrong");
        await dialogContent.findByText(/This action is forbidden/);
        await dialogContent.findByRole("button", { name: "Close" });
    },
};

export const WithButton = {
    render: () => <WithButtonStory />,
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(await canvas.findByText("Show Error Dialog"));

        const dialog = await screen.findByRole("dialog");
        await expect(dialog).toBeInTheDocument();

        const dialogContent = within(dialog);
        await dialogContent.findByText("Long Detailed Error");
        await dialogContent.findByText("Something went wrong");
        await dialogContent.findByText("This action is cannot be performed due to some very long reason that needs to wrap in this dialog for demonstration purposes.");
        await dialogContent.findByRole("button", { name: "Close" });
    },
};
