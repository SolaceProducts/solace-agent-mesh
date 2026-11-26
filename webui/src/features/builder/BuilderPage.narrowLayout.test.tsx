// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { SPLIT_MIN_WIDTH } from "./hooks/builderSplitLayout";
import type { BuildManifest } from "./hooks/useBuilderManifest";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub;

const manifestState = {
    manifest: null as BuildManifest | null,
    isLoading: false,
    isSettled: true,
    isUnreadable: false,
    saveManifest: vi.fn().mockResolvedValue(true),
};

vi.mock("./hooks/useBuilderManifest", async importOriginal => ({
    ...(await importOriginal<typeof import("./hooks/useBuilderManifest")>()),
    useBuilderManifest: () => manifestState,
}));

vi.mock(import("./BuilderCanvas"), () => ({
    BuilderCanvas: ({ isVisible }: { isVisible?: boolean }) => <div data-testid="canvas-stub" data-visible={String(isVisible)} />,
}));

const mobile = { value: false };
vi.mock("@/hooks", async importOriginal => ({
    ...(await importOriginal<typeof import("@/hooks")>()),
    useIsMobile: () => mobile.value,
}));

const { BuilderPage } = await import("./BuilderPage");

const server = setupServer(
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } })),
    http.get("*/api/v1/sessions/:id", () => new HttpResponse(null, { status: 404 })),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } }))
);

const GOOD = { name: "sys", components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "validated" }] } as unknown as BuildManifest;

let rowWidth = 1200;

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());
afterEach(() => {
    server.resetHandlers();
    vi.restoreAllMocks();
});

beforeEach(() => {
    manifestState.manifest = GOOD;
    manifestState.isUnreadable = false;
    mobile.value = false;
    rowWidth = 1200;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => ({ width: rowWidth }) as DOMRect);
});

function builderTree(sessionId = "S1") {
    return (
        <MemoryRouter initialEntries={["/builder"]}>
            <StoryProvider chatContextValues={{ isResponding: false, sessionId } as unknown as Partial<ChatContextValue>}>
                <BuilderPage />
            </StoryProvider>
        </MemoryRouter>
    );
}

function renderBuilder() {
    return render(builderTree());
}

function canvasIsShown(container: HTMLElement): boolean {
    const canvas = container.querySelector<HTMLElement>('[data-testid="builder-canvas-pane"]');
    expect(canvas).not.toBeNull();
    return !canvas!.parentElement!.classList.contains("hidden");
}

const chatPanel = () => screen.queryByTestId("builder-chat-panel");
const chatWrapperIsHidden = () => screen.getByTestId("builder-chat-wrapper").classList.contains("hidden");

describe("BuilderPage — narrow-window layout", () => {
    test("side by side, the chat is the split panel with its 320px minimum", async () => {
        const { container } = renderBuilder();
        await waitFor(() => expect(canvasIsShown(container)).toBe(true));
        expect(chatWrapperIsHidden()).toBe(false);
        expect(chatPanel()!.style.minWidth).toBe("320px");
        expect(screen.getByTestId("builder-chat-resize-handle")).toBeInTheDocument();
        expect(screen.getByTestId("canvas-stub").dataset.visible).toBe("true");
    });

    test("too narrow, the chat has the page to itself instead of a split panel", async () => {
        rowWidth = SPLIT_MIN_WIDTH - 1;
        renderBuilder();
        await screen.findByRole("button", { name: "View diagram" });
        expect(chatWrapperIsHidden()).toBe(false);
        expect(chatPanel()!.style.width).toBe("");
        expect(screen.queryByTestId("builder-chat-resize-handle")).toBeNull();
    });

    test("the canvas is told it is visible only while it is shown", async () => {
        rowWidth = SPLIT_MIN_WIDTH - 1;
        renderBuilder();
        const canvasVisible = () => screen.getByTestId("canvas-stub").dataset.visible;
        fireEvent.click(await screen.findByRole("button", { name: "View diagram" }));
        expect(canvasVisible()).toBe("true");

        fireEvent.click(screen.getByRole("button", { name: "Back to chat" }));
        expect(canvasVisible()).toBe("false");
    });

    test("in the diagram view the chat is hidden, and Back to chat shows it again", async () => {
        rowWidth = SPLIT_MIN_WIDTH - 1;
        renderBuilder();
        fireEvent.click(await screen.findByRole("button", { name: "View diagram" }));
        expect(chatWrapperIsHidden()).toBe(true);

        fireEvent.click(screen.getByRole("button", { name: "Back to chat" }));
        expect(chatWrapperIsHidden()).toBe(false);
    });

    test("the canvas pane keeps a 400px minimum width", async () => {
        const { container } = renderBuilder();
        await waitFor(() => expect(canvasIsShown(container)).toBe(true));
        expect(container.querySelector<HTMLElement>('[data-testid="builder-canvas-pane"]')!.style.minWidth).toBe("400px");
    });

    test("shows chat and canvas side by side with no toggle when both fit", async () => {
        const { container } = renderBuilder();
        await waitFor(() => expect(canvasIsShown(container)).toBe(true));
        expect(screen.queryByRole("button", { name: "View diagram" })).toBeNull();
    });

    test("shows the chat alone with a View diagram button when both do not fit", async () => {
        rowWidth = SPLIT_MIN_WIDTH - 1;
        const { container } = renderBuilder();
        await screen.findByRole("button", { name: "View diagram" });
        expect(canvasIsShown(container)).toBe(false);
    });

    test("View diagram swaps to the canvas alone and Back to chat returns", async () => {
        rowWidth = SPLIT_MIN_WIDTH - 1;
        const { container } = renderBuilder();
        fireEvent.click(await screen.findByRole("button", { name: "View diagram" }));
        expect(canvasIsShown(container)).toBe(true);
        expect(screen.queryByRole("button", { name: "Open chat" })).toBeNull();

        fireEvent.click(screen.getByRole("button", { name: "Back to chat" }));
        expect(canvasIsShown(container)).toBe(false);
    });

    test("mobile keeps the canvas hidden and offers no toggle", async () => {
        mobile.value = true;
        rowWidth = 400;
        const { container } = renderBuilder();
        await waitFor(() => expect(canvasIsShown(container)).toBe(false));
        expect(screen.queryByRole("button", { name: "View diagram" })).toBeNull();
    });

    test("offers no toggle when narrow and there is nothing to draw", async () => {
        manifestState.manifest = null;
        rowWidth = SPLIT_MIN_WIDTH - 1;
        renderBuilder();
        await waitFor(() => expect(screen.getByText("EXPERIMENTAL")).toBeInTheDocument());
        expect(screen.queryByRole("button", { name: "View diagram" })).toBeNull();
    });

    test("switching to another session while the canvas is showing returns to the chat", async () => {
        rowWidth = SPLIT_MIN_WIDTH - 1;
        const { container, rerender } = renderBuilder();
        fireEvent.click(await screen.findByRole("button", { name: "View diagram" }));
        expect(canvasIsShown(container)).toBe(true);

        rerender(builderTree("S2"));
        await screen.findByRole("button", { name: "View diagram" });
        expect(canvasIsShown(container)).toBe(false);
    });
});
