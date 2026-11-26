// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import { SharedSidePanel } from "@/features/share/SharedSidePanel";

import { documentSearchRagData } from "@/testing/data/ragSearchResults";
import { StoryProvider } from "@/testing/providers/StoryProvider";

describe("SharedSidePanel sources rendering parity with regular chat", () => {
    const baseProps = {
        isCollapsed: false,
        activeTab: "sources" as const,
        onTabChange: vi.fn(),
        onToggle: vi.fn(),
        onOpenTab: vi.fn(),
        hasRagSources: true,
        handleSharedArtifactDownload: vi.fn().mockResolvedValue(undefined),
        session: null,
        selectedTaskId: null,
        onTaskSelect: vi.fn(),
        ragData: documentSearchRagData,
    };

    const renderPanel = () =>
        render(
            <MemoryRouter>
                <StoryProvider>
                    <SharedSidePanel {...baseProps} />
                </StoryProvider>
            </MemoryRouter>
        );

    test("uses DocumentSourcesPanel (no sub-tabs) for document-search results — matching regular chat", async () => {
        renderPanel();

        expect(await screen.findByText(/3 Documents/)).toBeInTheDocument();
        expect(await screen.findByText(/6 Citations/)).toBeInTheDocument();
        expect(screen.queryByText(/6 Sources/)).not.toBeInTheDocument();
    });
});
