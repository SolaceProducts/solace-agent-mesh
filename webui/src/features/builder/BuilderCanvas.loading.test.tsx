// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { BuilderCanvas } from "./BuilderCanvas";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub;

const server = setupServer(http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })));
beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const manifestWithAgent: BuildManifest = {
    name: "build",
    components: [{ filename: "a.yaml", type: "agent", name: "Classifier", status: "draft", origin: "session" }],
};
const componentData = new Map<string, ComponentCanvasData>([
    [
        "Classifier",
        {
            name: "Classifier",
            filename: "a.yaml",
            type: "agent",
            status: "draft",
            origin: "session",
            yamlText: "ok",
            isLoading: false,
            isGenerating: false,
            isQueued: false,
            isWaitingForInput: false,
            isBuildingDeps: false,
            isBuilderResponding: false,
            dependencyProgress: [],
        },
    ],
]);

function canvasProps(props: Partial<Parameters<typeof BuilderCanvas>[0]>) {
    return { manifest: null, componentData: new Map(), selectedNodeId: null, onNodeSelect: () => {}, onNodeDoubleClick: () => {}, isEmpty: false, isLoading: false, ...props } as Parameters<typeof BuilderCanvas>[0];
}

function renderCanvas(props: Partial<Parameters<typeof BuilderCanvas>[0]>) {
    return render(
        <StoryProvider chatContextValues={{ sessionId: "s1" } as unknown as Partial<ChatContextValue>}>
            <div style={{ width: 800, height: 600 }}>
                <BuilderCanvas {...canvasProps(props)} />
            </div>
        </StoryProvider>
    );
}

const canvasMounted = (c: HTMLElement) => !!c.querySelector(".react-flow");

describe("BuilderCanvas loading gate", () => {
    test("shows the spinner while loading with an empty canvas (genuine first load)", async () => {
        const { container } = renderCanvas({ isLoading: true, manifest: null, componentData: new Map() });
        await waitFor(() => expect(screen.getByText("Loading configuration...")).toBeInTheDocument());
        expect(canvasMounted(container)).toBe(false);
    });

    test("does NOT swap in the spinner while loading once the canvas has nodes — the blink fix", async () => {
        const { container } = renderCanvas({ isLoading: true, manifest: manifestWithAgent, componentData });
        await waitFor(() => expect(canvasMounted(container)).toBe(true));
        expect(screen.queryByText("Loading configuration...")).not.toBeInTheDocument();
    });

    test("empty→populated while loading: spinner clears to the canvas and stays (the config→config handoff)", async () => {
        const { container, rerender } = renderCanvas({ isLoading: true, manifest: null, componentData: new Map() });
        await waitFor(() => expect(screen.getByText("Loading configuration...")).toBeInTheDocument());

        rerender(
            <StoryProvider chatContextValues={{ sessionId: "s1" } as unknown as Partial<ChatContextValue>}>
                <div style={{ width: 800, height: 600 }}>
                    <BuilderCanvas {...canvasProps({ isLoading: true, manifest: manifestWithAgent, componentData })} />
                </div>
            </StoryProvider>
        );

        await waitFor(() => expect(canvasMounted(container)).toBe(true));
        expect(screen.queryByText("Loading configuration...")).not.toBeInTheDocument();
    });
});
