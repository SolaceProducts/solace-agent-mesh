// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import LinearStepViewPanel from "@/features/activities/LinearStepViewPanel";

const ts = new Date().toISOString();
const baseStep = { rawEventIds: [] as string[], nestingLevel: 0, owningTaskId: "task-1" };

const mockArtifact = {
    filename: "report.md",
    mimeType: "text/markdown",
    size: 1024,
    lastModified: ts,
    version: 1,
};

const stepsWithArtifact = [
    {
        id: "s-1",
        type: "USER_REQUEST" as const,
        timestamp: ts,
        title: "User Input",
        data: { text: "Create a report" },
        ...baseStep,
    },
    {
        id: "s-2",
        type: "AGENT_ARTIFACT_NOTIFICATION" as const,
        timestamp: ts,
        title: "Created Artifact - report.md",
        data: {
            artifactNotification: {
                artifactName: "report.md",
                version: 2,
                mimeType: "text/markdown",
                description: "A generated report",
            },
        },
        ...baseStep,
    },
];

const meta = {
    title: "Activities/LinearStepViewPanel",
    component: LinearStepViewPanel,
    parameters: {
        taskContext: {
            highlightedStepId: null,
        },
    },
    decorators: [
        Story => (
            <div className="ml-auto h-screen w-2/5 border-l p-4">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof LinearStepViewPanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {
    args: { steps: [] },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("No events to display.")).toBeInTheDocument();
    },
};

export const WithSteps: Story = {
    args: { steps: stepsWithArtifact },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText("Events")).toBeInTheDocument();
        expect(canvas.getByText("User Input")).toBeInTheDocument();
        expect(canvas.getByText("View File")).toBeInTheDocument();
    },
};

export const ViewFileWithCustomHandler: Story = {
    args: {
        steps: stepsWithArtifact,
        artifactLookup: fn(() => mockArtifact),
        onViewArtifact: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);

        const viewFileButton = canvas.getByText("View File");
        expect(viewFileButton).toBeInTheDocument();

        await userEvent.click(viewFileButton);

        expect(args.artifactLookup).toHaveBeenCalledWith("report.md");
        expect(args.onViewArtifact).toHaveBeenCalledWith(expect.objectContaining({ filename: "report.md" }), 2);
    },
};

export const ViewFileArtifactNotFound: Story = {
    args: {
        steps: stepsWithArtifact,
        artifactLookup: fn(() => undefined),
        onViewArtifact: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);

        await userEvent.click(canvas.getByText("View File"));

        expect(args.artifactLookup).toHaveBeenCalledWith("report.md");
        expect(args.onViewArtifact).not.toHaveBeenCalled();
    },
};
