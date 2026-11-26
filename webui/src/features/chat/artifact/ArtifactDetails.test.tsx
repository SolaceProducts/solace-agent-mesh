// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { ChatContext, type ChatContextValue } from "@/providers/contexts/ChatContext";

import { ArtifactDetails } from "@/features/chat/artifact/ArtifactDetails";

import type { ArtifactInfo } from "@/types";

const baseArtifact: ArtifactInfo = {
    filename: "report.pdf",
    mimeType: "application/pdf",
    size: 1024,
    lastModified: "2024-01-01T00:00:00.000Z",
    uri: "artifact://report.pdf",
};

function renderWithProviders(ui: React.ReactNode, contextValue?: Partial<ChatContextValue>) {
    const queryClient = new QueryClient();
    const content = contextValue ? <ChatContext.Provider value={contextValue as ChatContextValue}>{ui}</ChatContext.Provider> : ui;
    return render(<QueryClientProvider client={queryClient}>{content}</QueryClientProvider>);
}

describe("ArtifactDetails outside a ChatProvider", () => {
    test("renders filename without throwing when no ChatContext is present", () => {
        renderWithProviders(<ArtifactDetails artifactInfo={baseArtifact} isPreview />);
        expect(screen.getByText("report.pdf")).toBeInTheDocument();
    });

    test("hides version selector when no ChatContext is present even with isPreview=true", () => {
        renderWithProviders(<ArtifactDetails artifactInfo={baseArtifact} isPreview />);
        expect(screen.queryByText("Version")).not.toBeInTheDocument();
    });

    test("invokes onDownload when download button is clicked", () => {
        const onDownload = vi.fn();
        renderWithProviders(<ArtifactDetails artifactInfo={baseArtifact} onDownload={onDownload} isPreview />);
        fireEvent.click(screen.getByRole("button", { name: /download/i }));
        expect(onDownload).toHaveBeenCalledWith(baseArtifact);
    });

    test("invokes onDelete when delete button is clicked", () => {
        const onDelete = vi.fn();
        renderWithProviders(<ArtifactDetails artifactInfo={baseArtifact} onDelete={onDelete} isPreview />);
        fireEvent.click(screen.getByRole("button", { name: /delete/i }));
        expect(onDelete).toHaveBeenCalledTimes(1);
    });
});

describe("ArtifactDetails inside a ChatProvider", () => {
    test("hides version selector when only one version is available", () => {
        renderWithProviders(<ArtifactDetails artifactInfo={baseArtifact} isPreview />, {
            previewedArtifactAvailableVersions: [1],
            currentPreviewedVersionNumber: 1,
            navigateArtifactVersion: vi.fn(),
        });
        expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    });

    test("renders version selector when multiple versions are available and isPreview=true", () => {
        renderWithProviders(<ArtifactDetails artifactInfo={baseArtifact} isPreview />, {
            previewedArtifactAvailableVersions: [1, 2, 3],
            currentPreviewedVersionNumber: 2,
            navigateArtifactVersion: vi.fn(),
        });
        expect(screen.getByRole("combobox")).toBeInTheDocument();
    });

    test("hides version selector when isPreview=false even with multiple versions", () => {
        renderWithProviders(<ArtifactDetails artifactInfo={baseArtifact} />, {
            previewedArtifactAvailableVersions: [1, 2, 3],
            currentPreviewedVersionNumber: 2,
            navigateArtifactVersion: vi.fn(),
        });
        expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    });
});

describe("ArtifactDetails download status", () => {
    test("disables the download button and shows the spinner while its download is in flight, then re-enables", async () => {
        let resolveDownload: () => void = () => {};
        const onDownload = vi.fn(
            () =>
                new Promise<void>(resolve => {
                    resolveDownload = resolve;
                })
        );

        renderWithProviders(<ArtifactDetails artifactInfo={baseArtifact} onDownload={onDownload} isPreview />);

        const downloadButton = () => screen.getByRole("button", { name: /download/i });

        fireEvent.click(downloadButton());

        await waitFor(() => expect(downloadButton()).toBeDisabled());
        expect(downloadButton()).toHaveAttribute("aria-busy", "true");

        resolveDownload();

        await waitFor(() => expect(downloadButton()).not.toBeDisabled());
    });
});
