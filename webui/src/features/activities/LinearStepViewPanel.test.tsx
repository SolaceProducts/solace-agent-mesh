// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { SeedSessionArtifacts } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { ArtifactInfo } from "@/types";

import LinearStepViewPanel from "./LinearStepViewPanel";

const ts = new Date().toISOString();
const baseStep = { rawEventIds: [] as string[], nestingLevel: 0, owningTaskId: "task-1" };

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

const mockArtifact = {
    filename: "report.md",
    mime_type: "text/markdown",
    size: 1024,
    last_modified: ts,
    version: 1,
};

function renderPanel(props: Partial<React.ComponentProps<typeof LinearStepViewPanel>> = {}, chatContext = {}) {
    return render(
        <MemoryRouter>
            <StoryProvider chatContextValues={{ sessionId: "test", ...chatContext }} taskContextValues={{}}>
                <LinearStepViewPanel steps={stepsWithArtifact} {...props} />
            </StoryProvider>
        </MemoryRouter>
    );
}

beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
    vi.useRealTimers();
});

describe("LinearStepViewPanel", () => {
    test("scrolls to highlighted step when highlightedStepId is set", () => {
        const scrollIntoViewMock = vi.fn();
        Element.prototype.scrollIntoView = scrollIntoViewMock;

        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "test" }} taskContextValues={{ highlightedStepId: "s-2" }}>
                    <LinearStepViewPanel steps={stepsWithArtifact} />
                </StoryProvider>
            </MemoryRouter>
        );

        expect(scrollIntoViewMock).toHaveBeenCalledWith({ behavior: "smooth", block: "nearest" });
    });

    test("renders empty state when no steps", () => {
        render(
            <MemoryRouter>
                <StoryProvider taskContextValues={{}}>
                    <LinearStepViewPanel steps={[]} />
                </StoryProvider>
            </MemoryRouter>
        );

        expect(screen.getByText("No events to display.")).toBeInTheDocument();
    });

    test("renders steps with Events heading and step cards", () => {
        const { container } = renderPanel();

        expect(screen.getByText("Events")).toBeInTheDocument();
        expect(screen.getByText("User Input")).toBeInTheDocument();
        expect(screen.getByText("Created Artifact - report.md")).toBeInTheDocument();
        expect(screen.getByText("View File")).toBeInTheDocument();

        const stepContainer = container.querySelector(".space-y-2");
        expect(stepContainer).not.toBeNull();
        expect(stepContainer!.children.length).toBe(3);
    });

    test("uses artifactLookup and onViewArtifact when clicking View File", async () => {
        const artifactLookup = vi.fn().mockReturnValue(mockArtifact);
        const onViewArtifact = vi.fn();

        renderPanel({ artifactLookup, onViewArtifact });

        await userEvent.setup({ advanceTimers: vi.advanceTimersByTime }).click(screen.getByRole("button", { name: "View File" }));

        expect(artifactLookup).toHaveBeenCalledWith("report.md");
        expect(onViewArtifact).toHaveBeenCalledWith(mockArtifact, 2);
    });

    test("does not call onViewArtifact when artifactLookup returns undefined", async () => {
        const artifactLookup = vi.fn().mockReturnValue(undefined);
        const onViewArtifact = vi.fn();

        renderPanel({ artifactLookup, onViewArtifact });

        await userEvent.setup({ advanceTimers: vi.advanceTimersByTime }).click(screen.getByRole("button", { name: "View File" }));

        expect(artifactLookup).toHaveBeenCalledWith("report.md");
        expect(onViewArtifact).not.toHaveBeenCalled();
    });

    test("passes version as undefined when step has no version", async () => {
        const stepsNoVersion = [
            {
                id: "s-3",
                type: "AGENT_ARTIFACT_NOTIFICATION" as const,
                timestamp: ts,
                title: "Created Artifact - notes.txt",
                data: {
                    artifactNotification: {
                        artifactName: "notes.txt",
                        mimeType: "text/plain",
                    },
                },
                ...baseStep,
            },
        ];
        const artifactLookup = vi.fn().mockReturnValue(mockArtifact);
        const onViewArtifact = vi.fn();

        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "test" }} taskContextValues={{}}>
                    <LinearStepViewPanel steps={stepsNoVersion} artifactLookup={artifactLookup} onViewArtifact={onViewArtifact} />
                </StoryProvider>
            </MemoryRouter>
        );

        await userEvent.setup({ advanceTimers: vi.advanceTimersByTime }).click(screen.getByRole("button", { name: "View File" }));

        expect(onViewArtifact).toHaveBeenCalledWith(mockArtifact, undefined);
    });

    test("falls back to ChatContext when props not provided", async () => {
        const setPreviewArtifact = vi.fn();

        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "test", setPreviewArtifact }} taskContextValues={{}}>
                    <SeedSessionArtifacts sessionId="test" artifacts={[mockArtifact as unknown as ArtifactInfo]} />
                    <LinearStepViewPanel steps={stepsWithArtifact} />
                </StoryProvider>
            </MemoryRouter>
        );

        await userEvent.setup({ advanceTimers: vi.advanceTimersByTime }).click(screen.getByRole("button", { name: "View File" }));

        expect(setPreviewArtifact).toHaveBeenCalledWith(mockArtifact);
    });

    test("keeps View File on the surfaces that do not pass the prop", () => {
        renderPanel();
        expect(screen.getByRole("button", { name: "View File" })).toBeInTheDocument();
    });
});
