// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, waitFor, within } from "storybook/test";

import { ExampleArtifactField } from "@/features/evaluations/components";

const meta = {
    title: "Evaluations/ExampleArtifactField",
    component: ExampleArtifactField,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component:
                    "The one optional input file on a dataset example. Empty renders an Attach button; occupied renders a single chip with replace and remove. Errors display locally rather than through the chat provider so the field works on the dataset pages.",
            },
        },
    },
} satisfies Meta<typeof ExampleArtifactField>;

export default meta;
type Story = StoryObj<typeof meta>;

const yamlFile = () => new File(["name: acme\nreplicas: 2\n"], "values.yaml", { type: "application/yaml" });

export const EmptyDropZone: Story = {
    args: { onUpload: fn(async () => {}) },
};

export const CompactEmpty: Story = {
    args: { onUpload: fn(async () => {}) },
    play: async ({ args, canvasElement }) => {
        const input = within(canvasElement).getByLabelText("Attach input file") as HTMLInputElement;
        await userEvent.upload(input, yamlFile());
        await waitFor(() => expect(args.onUpload).toHaveBeenCalledTimes(1));
    },
};

export const Occupied: Story = {
    args: {
        artifact: { filename: "values.yaml", mimeType: "application/yaml", size: 84, version: 0 },
        onUpload: fn(async () => {}),
        onRemove: fn(async () => {}),
        onDownload: fn(async () => {}),
    },
    play: async ({ args, canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByText("values.yaml"));
        await waitFor(() => expect(args.onDownload).toHaveBeenCalledTimes(1));
    },
};

export const ReadOnlyOccupied: Story = {
    args: {
        artifact: { filename: "frozen.yaml", version: 0, size: 84 },
        onDownload: fn(async () => {}),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.queryByText("Replace")).toBeNull();
    },
};

export const UploadFailed: Story = {
    args: {
        onUpload: fn(async () => {
            throw new Error("file exceeds the 10000000 byte limit");
        }),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.upload(canvas.getByLabelText("Attach input file") as HTMLInputElement, yamlFile());
        await waitFor(async () => await expect(canvas.getByRole("alert").textContent).toContain("file exceeds the 10000000 byte limit"));
    },
};
