// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta } from "@storybook/react-vite";
import { expect, screen, userEvent, within } from "storybook/test";

import { Button } from "@/ui/button";

import { ConfirmationDialog } from "./ConfirmationDialog";

const meta = {
    title: "Common/ConfirmationDialog",
    component: ConfirmationDialog,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "A confirmation dialog component with customizable title, message, and actions",
            },
        },
    },
} satisfies Meta<typeof ConfirmationDialog>;

export default meta;

function DefaultStory() {
    const [open, setOpen] = useState(true);
    return <ConfirmationDialog title="Confirm Dialog" content="Are you sure you want to do this action?" onConfirm={() => alert("Action confirmed")} open={open} onOpenChange={setOpen} />;
}

function WithTriggerStory() {
    const [open, setOpen] = useState(false);
    return (
        <ConfirmationDialog
            title="Confirm Dialog"
            trigger={
                <Button variant="outline" className="w-50">
                    Trigger
                </Button>
            }
            content="Are you sure you want to do this action?"
            onConfirm={() => alert("Action confirmed")}
            open={open}
            onOpenChange={setOpen}
        />
    );
}

function WithExternalButtonStory() {
    const [open, setOpen] = useState(false);
    return (
        <>
            <Button onClick={() => setOpen(true)} className="w-50">
                External Button
            </Button>
            <ConfirmationDialog title="Confirm Dialog With External Button" content="Are you sure you want to do this action?" onConfirm={() => alert("Action confirmed")} open={open} onOpenChange={setOpen} />
        </>
    );
}

export const Default = {
    render: () => <DefaultStory />,
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await expect(dialog).toBeInTheDocument();

        const dialogContent = within(dialog);
        await dialogContent.findByText("Confirm Dialog");
        await dialogContent.findByText("Are you sure you want to do this action?");
        await dialogContent.findByRole("button", { name: "Cancel" });
        await dialogContent.findByRole("button", { name: "Confirm" });
    },
};

export const WithTrigger = {
    render: () => <WithTriggerStory />,
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(await canvas.findByText("Trigger"));

        const dialog = await screen.findByRole("dialog");
        await expect(dialog).toBeInTheDocument();

        const dialogContent = within(dialog);
        await dialogContent.findByText("Confirm Dialog");
        await dialogContent.findByRole("button", { name: "Cancel" });
        await dialogContent.findByRole("button", { name: "Confirm" });
    },
};

export const WithExternalButton = {
    render: () => <WithExternalButtonStory />,
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(await canvas.findByText("External Button"));

        const dialog = await screen.findByRole("dialog");
        await expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);
        await dialogContent.findByRole("button", { name: "Cancel" });
        await dialogContent.findByRole("button", { name: "Confirm" });
    },
};
