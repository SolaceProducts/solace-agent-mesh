// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { BuilderPageState } from "./useBuilderPageState";

const mockGet = vi.fn();
const handleNewSession = vi.fn();
const scenario = { sessionId: "S1" };

const deferred = <T,>() => {
    let resolve!: (v: T) => void;
    let reject!: (e: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return { promise, resolve, reject };
};

const state = { rootName: "New Build" } as unknown as BuilderPageState;

describe("BuilderPageHeader — title reset", () => {
    let BuilderPageHeader: React.ComponentType<{ state: BuilderPageState; canvasViewToggle?: { showingCanvas: boolean; onToggle: () => void } | null }>;

    beforeEach(async () => {
        vi.resetModules();
        mockGet.mockReset();
        handleNewSession.mockReset();
        scenario.sessionId = "S1";

        vi.doMock("@/api/client", () => ({ api: { webui: { get: mockGet } } }));
        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({ handleNewSession, sessionId: scenario.sessionId }),
        }));
        vi.doMock("@/hooks", () => ({ useIsMobile: () => false }));

        ({ BuilderPageHeader } = await import("./BuilderPageHeader"));
    });

    test("a session-name fetch that resolves after the session is cleared does not leak the old title", async () => {
        const d = deferred<{ name: string }>();
        mockGet.mockReturnValue(d.promise);

        const { rerender } = render(<BuilderPageHeader state={state} />);
        expect(mockGet).toHaveBeenCalledWith("/api/v1/sessions/S1", expect.objectContaining({ signal: expect.anything() }));

        scenario.sessionId = "";
        rerender(<BuilderPageHeader state={state} />);

        d.resolve({ name: "Weather in Paris" });
        await Promise.resolve();
        await Promise.resolve();

        expect(screen.getByText("New Build")).toBeInTheDocument();
        expect(screen.queryByText("Weather in Paris")).not.toBeInTheDocument();
    });

    test("renders the EXPERIMENTAL lifecycle badge", async () => {
        mockGet.mockReturnValue(deferred<{ name: string }>().promise);
        render(<BuilderPageHeader state={state} />);
        expect(screen.getByText("EXPERIMENTAL")).toBeInTheDocument();
    });

    test("shows the fetched session name for the active session", async () => {
        const d = deferred<{ name: string }>();
        mockGet.mockReturnValue(d.promise);

        render(<BuilderPageHeader state={state} />);
        d.resolve({ name: "My Weather Build" });

        await waitFor(() => expect(screen.getByText("My Weather Build")).toBeInTheDocument());
    });

    test("a benign 404 (session not yet persisted) is not logged", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
        const d = deferred<{ name: string }>();
        mockGet.mockReturnValue(d.promise);

        render(<BuilderPageHeader state={state} />);
        d.reject({ status: 404 });
        await Promise.resolve();
        await Promise.resolve();

        expect(warn).not.toHaveBeenCalled();
        expect(screen.getByText("New Build")).toBeInTheDocument();
        warn.mockRestore();
    });

    test("an unexpected (non-404) lookup failure is logged", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
        const d = deferred<{ name: string }>();
        mockGet.mockReturnValue(d.promise);

        render(<BuilderPageHeader state={state} />);
        d.reject({ status: 500 });
        await Promise.resolve();
        await Promise.resolve();

        expect(warn).toHaveBeenCalledWith("[BuilderPageHeader] session lookup failed", expect.objectContaining({ sessionId: "S1" }));
        warn.mockRestore();
    });

    test("offers no chat/canvas toggle by default", () => {
        mockGet.mockReturnValue(deferred<{ name: string }>().promise);
        render(<BuilderPageHeader state={state} />);
        expect(screen.queryByRole("button", { name: /view diagram|back to chat/i })).not.toBeInTheDocument();
    });

    test("offers a View diagram button that toggles the canvas view when the split does not fit", () => {
        mockGet.mockReturnValue(deferred<{ name: string }>().promise);
        const onToggle = vi.fn();
        render(<BuilderPageHeader state={state} canvasViewToggle={{ showingCanvas: false, onToggle }} />);
        fireEvent.click(screen.getByRole("button", { name: "View diagram" }));
        expect(onToggle).toHaveBeenCalledTimes(1);
    });

    test("offers Back to chat while the canvas is showing", () => {
        mockGet.mockReturnValue(deferred<{ name: string }>().promise);
        render(<BuilderPageHeader state={state} canvasViewToggle={{ showingCanvas: true, onToggle: vi.fn() }} />);
        expect(screen.getByRole("button", { name: "Back to chat" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "View diagram" })).not.toBeInTheDocument();
    });
});
