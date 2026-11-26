// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test } from "vitest";

import { DocumentSourcesPanel } from "@/features/chat/rag/DocumentSourcesPanel";

import { documentSearchRagData } from "@/testing/data/ragSearchResults";
import { StoryProvider } from "@/testing/providers/StoryProvider";

describe("DocumentSourcesPanel citation drill-in gating", () => {
    const renderPanel = (readOnly: boolean) =>
        render(
            <MemoryRouter>
                <StoryProvider>
                    <DocumentSourcesPanel ragData={documentSearchRagData} enabled={true} readOnly={readOnly} />
                </StoryProvider>
            </MemoryRouter>
        );

    test("offers the citation 'View' drill-in in the default (owner) mode", async () => {
        renderPanel(false);
        const trigger = await screen.findByRole("button", { name: /quarterly_report_q4_2024\.pdf/ });
        fireEvent.click(trigger);
        expect(trigger).toHaveAttribute("aria-expanded", "true");
        expect((await screen.findAllByRole("button", { name: /^View/ })).length).toBeGreaterThan(0);
    });

    test("hides the 'View' drill-in in read-only mode so no owner-scoped fetch fires", async () => {
        renderPanel(true);
        const trigger = await screen.findByRole("button", { name: /quarterly_report_q4_2024\.pdf/ });
        fireEvent.click(trigger);
        expect(trigger).toHaveAttribute("aria-expanded", "true");
        expect(screen.getByText(/3 Documents/)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /^View/ })).not.toBeInTheDocument();
    });
});
