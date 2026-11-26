// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { BuilderCanvas } from "./BuilderCanvas";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}

beforeEach(() => {
    vi.stubGlobal("ResizeObserver", ResizeObserverStub);
});

const manifest: BuildManifest = {
    name: "build",
    components: [
        { filename: "a.yaml", type: "agent", name: "AlphaAgent", status: "draft", origin: "session" },
        { filename: "b.yaml", type: "agent", name: "BetaAgent", status: "draft", origin: "session" },
    ],
};

function canvasDataFor(component: { name: string; filename: string }, generating: string | null): ComponentCanvasData {
    return {
        name: component.name,
        filename: component.filename,
        type: "agent",
        status: "draft",
        origin: "session",
        yamlText: "ok",
        isLoading: false,
        isGenerating: component.name === generating,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
    };
}

function canvas(generating: string | null) {
    const componentData = new Map<string, ComponentCanvasData>();
    for (const c of manifest.components ?? []) componentData.set(c.name, canvasDataFor(c, generating));
    const props: Parameters<typeof BuilderCanvas>[0] = { manifest, componentData, selectedNodeId: null, onNodeSelect: () => {}, onNodeDoubleClick: () => {}, isEmpty: false, isLoading: false };
    return (
        <StoryProvider chatContextValues={{ sessionId: "s1" }}>
            <div style={{ width: 800, height: 600 }}>
                <BuilderCanvas {...props} />
            </div>
        </StoryProvider>
    );
}

const selectedTab = () => screen.getAllByRole("tab").find(t => t.getAttribute("aria-selected") === "true");

describe("BuilderCanvas group tabs follow the generating group until the user picks a tab", () => {
    test("without a click the canvas follows the group that starts generating", async () => {
        const { rerender } = render(canvas(null));
        await waitFor(() => expect(selectedTab()).toHaveAccessibleName(/^AlphaAgent,/));

        rerender(canvas("BetaAgent"));

        await waitFor(() => expect(selectedTab()).toHaveAccessibleName(/^BetaAgent,/));
    });

    test("clicking the tab you are already on pins the view, so a later generating group does not pull it away", async () => {
        const user = userEvent.setup();
        const { rerender } = render(canvas(null));
        await waitFor(() => expect(selectedTab()).toHaveAccessibleName(/^AlphaAgent,/));

        await user.click(selectedTab()!);
        rerender(canvas("BetaAgent"));

        expect(selectedTab()).toHaveAccessibleName(/^AlphaAgent,/);
    });
});
