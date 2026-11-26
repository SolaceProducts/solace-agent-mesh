// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import { ArtifactCard } from "@/features/chat/artifact/ArtifactCard";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { ArtifactInfo } from "@/types";

const mockArtifact: ArtifactInfo = {
    filename: "report.pdf",
    mimeType: "application/pdf",
    size: 1024,
    lastModified: "2024-01-01T00:00:00.000Z",
    uri: "artifact://report.pdf",
};

function renderCard(props: Partial<React.ComponentProps<typeof ArtifactCard>> = {}, chatContextValues = {}) {
    return render(
        <MemoryRouter>
            <StoryProvider chatContextValues={chatContextValues}>
                <ArtifactCard artifact={mockArtifact} {...props} />
            </StoryProvider>
        </MemoryRouter>
    );
}

function getCardDiv() {
    return screen.getAllByRole("button").find(el => el.tagName === "DIV")!;
}

describe("ArtifactCard", () => {
    test("renders the artifact filename", () => {
        renderCard();
        expect(screen.getByText("report.pdf")).toBeInTheDocument();
    });

    test("has role=button and tabIndex=0 when not in preview mode", () => {
        renderCard({ isPreview: false });
        expect(getCardDiv()).toHaveAttribute("tabindex", "0");
    });

    test("has no role=button div when in preview mode", () => {
        renderCard({ isPreview: true });
        const divButtons = screen.getAllByRole("button").filter(el => el.tagName === "DIV");
        expect(divButtons).toHaveLength(0);
    });

    test("clicking the card calls setPreviewArtifact when not in preview mode", () => {
        const setPreviewArtifact = vi.fn();
        renderCard({ isPreview: false }, { setPreviewArtifact });
        fireEvent.click(getCardDiv());
        expect(setPreviewArtifact).toHaveBeenCalledWith(mockArtifact);
    });

    test("clicking does not call setPreviewArtifact when in preview mode", () => {
        const setPreviewArtifact = vi.fn();
        renderCard({ isPreview: true }, { setPreviewArtifact });
        const container = screen.getByText("report.pdf").closest("div")!;
        fireEvent.click(container);
        expect(setPreviewArtifact).not.toHaveBeenCalled();
    });

    test("pressing Enter calls setPreviewArtifact when not in preview mode", () => {
        const setPreviewArtifact = vi.fn();
        renderCard({ isPreview: false }, { setPreviewArtifact });
        fireEvent.keyDown(getCardDiv(), { key: "Enter" });
        expect(setPreviewArtifact).toHaveBeenCalledWith(mockArtifact);
    });

    test("pressing Space calls setPreviewArtifact when not in preview mode", () => {
        const setPreviewArtifact = vi.fn();
        renderCard({ isPreview: false }, { setPreviewArtifact });
        fireEvent.keyDown(getCardDiv(), { key: " " });
        expect(setPreviewArtifact).toHaveBeenCalledWith(mockArtifact);
    });

    test("shows a download button for a completed artifact", () => {
        renderCard();
        expect(screen.getByRole("button", { name: /download/i })).toBeInTheDocument();
    });

    test("hides the download button while the artifact is still being created", () => {
        renderCard({ artifact: { ...mockArtifact, creationStatus: "in-progress" } });
        expect(screen.queryByRole("button", { name: /download/i })).not.toBeInTheDocument();
        expect(screen.getByText(/creating/i)).toBeInTheDocument();
    });

    test("hides the download button for a failed artifact", () => {
        renderCard({ artifact: { ...mockArtifact, creationStatus: "failed" } });
        expect(screen.queryByRole("button", { name: /download/i })).not.toBeInTheDocument();
    });
});
