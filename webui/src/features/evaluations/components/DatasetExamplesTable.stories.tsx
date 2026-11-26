// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { DatasetExamplesTable } from "@/features/evaluations/components/DatasetExamplesTable";

const meta = {
    title: "Evaluations/DatasetExamplesTable",
    component: DatasetExamplesTable,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component:
                    "Read-only example list shared by the dataset side panel and the run-details snapshot dialog. The Input File and Expected File columns appear only when at least one row carries a file, so text-only datasets look unchanged.",
            },
        },
    },
} satisfies Meta<typeof DatasetExamplesTable>;

export default meta;
type Story = StoryObj<typeof meta>;

export const TextOnly: Story = {
    args: {
        rows: [
            { id: "1", prompt: "What is the capital of France?", expectedResponse: "Paris" },
            { id: "2", prompt: "Summarize the quarterly report.", expectedResponse: null },
        ],
    },
    play: async ({ canvasElement }) => {
        await expect(within(canvasElement).queryByText("Input File")).toBeNull();
        await expect(within(canvasElement).queryByText("Expected File")).toBeNull();
    },
};

export const ColumnKeptOnAPageWithNoFiles: Story = {
    args: {
        showArtifacts: true,
        rows: [
            { id: "1", prompt: "What is the capital of France?", expectedResponse: "Paris" },
            { id: "2", prompt: "Summarize the quarterly report.", expectedResponse: null },
        ],
    },
    play: async ({ canvasElement }) => {
        await expect(within(canvasElement).getByText("Input File")).toBeTruthy();
        await expect(within(canvasElement).getByText("Expected File")).toBeTruthy();
    },
};

export const NarrowContainerLongFilename: Story = {
    args: {
        showArtifacts: true,
        rows: [
            {
                id: "1",
                prompt: "Read the attached file and summarise it.",
                expectedResponse: "a summary",
                inputArtifact: { filename: "production-deployment-config-eu-west-1.yaml", version: 0, size: 2048 },
                expectedArtifact: { filename: "expected-summary-of-the-deployment-config.md", version: 0, size: 512 },
            },
        ],
    },
    decorators: [Story => <div style={{ width: 355 }}>{Story()}</div>],
    play: async ({ canvasElement }) => {
        const table = canvasElement.querySelector("table");
        if (!table) throw new Error("table did not render");
        const scroller = table.parentElement;
        if (!scroller) throw new Error("table has no scroll container");
        await expect(table.scrollWidth).toBeLessThanOrEqual(scroller.clientWidth);
    },
};

export const WithInputFiles: Story = {
    args: {
        showArtifacts: true,
        rows: [
            { id: "1", prompt: "Review this Helm values file.", expectedResponse: "Flags the missing resource limits.", inputArtifact: { filename: "values.yaml", version: 0, size: 84 } },
            { id: "2", prompt: "No file on this one.", expectedResponse: null },
        ],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText("Input File")).toBeTruthy();
        await expect(canvas.getByText("values.yaml")).toBeTruthy();
    },
};

export const WithBothRoles: Story = {
    args: {
        showArtifacts: true,
        rows: [
            {
                id: "1",
                prompt: "Review this Helm values file.",
                expectedResponse: null,
                inputArtifact: { filename: "values.yaml", version: 0, size: 84 },
                expectedArtifact: { filename: "reference.md", version: 0, size: 12 },
            },
        ],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText("Input File")).toBeTruthy();
        await expect(canvas.getByText("Expected File")).toBeTruthy();
        await expect(canvas.getByText("values.yaml")).toBeTruthy();
        await expect(canvas.getByText("reference.md")).toBeTruthy();
    },
};
