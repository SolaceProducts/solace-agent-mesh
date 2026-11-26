// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OoxmlDocumentRenderer } from "@/features/chat/preview/renderers/OoxmlDocumentRenderer";
import { withTimeout } from "@/features/chat/preview/renderers/withTimeout";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const h = vi.hoisted(() => ({
    docxCtor: vi.fn(),
    pptxCtor: vi.fn(),
    xlsxCtor: vi.fn(),
    load: vi.fn(),
    destroy: vi.fn(),
}));
vi.mock("@silurus/ooxml/docx", () => ({
    DocxScrollViewer: class {
        load = h.load;
        destroy = h.destroy;
        constructor(el: HTMLElement, opts: unknown) {
            h.docxCtor(el, opts);
        }
    },
}));
vi.mock("@silurus/ooxml/pptx", () => ({
    PptxScrollViewer: class {
        load = h.load;
        destroy = h.destroy;
        constructor(el: HTMLElement, opts: unknown) {
            h.pptxCtor(el, opts);
        }
    },
}));
vi.mock("@silurus/ooxml/xlsx", () => ({
    XlsxViewer: class {
        load = h.load;
        destroy = h.destroy;
        constructor(el: HTMLElement, opts: unknown) {
            h.xlsxCtor(el, opts);
        }
    },
}));

const uniqueB64 = () => btoa(`doc-${Math.random()}-${Date.now()}`);

function renderPreview(props: Partial<React.ComponentProps<typeof OoxmlDocumentRenderer>> = {}, flagOverrides: Record<string, boolean> = {}) {
    const setRenderError = vi.fn();
    const defaultProps: React.ComponentProps<typeof OoxmlDocumentRenderer> = {
        content: uniqueB64(),
        filename: "doc.docx",
        documentType: "docx",
        setRenderError,
    };
    const utils = render(
        <MemoryRouter>
            <StoryProvider flagValues={{ binary_artifact_preview: true, ...flagOverrides }}>
                <OoxmlDocumentRenderer {...defaultProps} {...props} />
            </StoryProvider>
        </MemoryRouter>
    );
    return { ...utils, setRenderError };
}

describe("OoxmlDocumentRenderer", () => {
    beforeEach(() => {
        h.load.mockResolvedValue(undefined);
    });
    afterEach(() => {
        vi.clearAllMocks();
    });

    it("mounts the docx viewer and clears the error on success", async () => {
        const { setRenderError } = renderPreview({ documentType: "docx" });
        await waitFor(() => expect(h.docxCtor).toHaveBeenCalled());
        await waitFor(() => expect(h.load).toHaveBeenCalled());
        expect(setRenderError).toHaveBeenLastCalledWith(null);
    });

    it("routes pptx to the pptx viewer", async () => {
        renderPreview({ documentType: "pptx", filename: "deck.pptx" });
        await waitFor(() => expect(h.pptxCtor).toHaveBeenCalled());
    });

    it("routes xlsx to the xlsx viewer", async () => {
        renderPreview({ documentType: "xlsx", filename: "book.xlsx" });
        await waitFor(() => expect(h.xlsxCtor).toHaveBeenCalled());
    });

    it("keeps hyperlinks disabled on the mounted viewer (XSS surface)", async () => {
        renderPreview({ documentType: "docx" });
        await waitFor(() => expect(h.docxCtor).toHaveBeenCalled());
        const opts = h.docxCtor.mock.calls[0][1] as { enableHyperlinks?: boolean };
        expect(opts.enableHyperlinks).toBe(false);
    });

    it("shows a disabled message and mounts nothing when the flag is off", async () => {
        const { getByText } = renderPreview({ documentType: "docx" }, { binary_artifact_preview: false });
        expect(getByText(/disabled by your administrator/i)).toBeTruthy();
        expect(h.docxCtor).not.toHaveBeenCalled();
    });

    it("reports an error when the viewer fails to load", async () => {
        h.load.mockRejectedValueOnce(new Error("corrupt file"));
        const { setRenderError } = renderPreview({ documentType: "docx" });
        await waitFor(() => expect(setRenderError).toHaveBeenCalledWith("corrupt file"));
    });

    it("reports an error when content is empty", async () => {
        const { setRenderError } = renderPreview({ content: "" });
        await waitFor(() => expect(setRenderError).toHaveBeenCalledWith("No document content to preview."));
    });

    it("destroys the viewer (no leak) and surfaces no error when unmounted mid-load", async () => {
        h.load.mockReturnValue(new Promise<void>(() => {}));
        const { unmount, setRenderError } = renderPreview({ documentType: "docx" });
        await waitFor(() => expect(h.docxCtor).toHaveBeenCalled());
        unmount();
        await waitFor(() => expect(h.destroy).toHaveBeenCalled());
        expect(setRenderError).not.toHaveBeenCalledWith("Preview timed out while rendering.");
    });

    it("reports the timeout and tears down the viewer when load never settles", async () => {
        vi.useFakeTimers();
        try {
            h.load.mockReturnValue(new Promise<void>(() => {}));
            const { setRenderError } = renderPreview({ documentType: "docx" });
            await act(async () => {
                await vi.advanceTimersByTimeAsync(30_000);
            });
            expect(setRenderError).toHaveBeenCalledWith("Preview timed out while rendering.");
            expect(h.destroy).toHaveBeenCalled();
        } finally {
            vi.useRealTimers();
        }
    });

    it("reports the error and destroys the viewer when the engine invokes onError", async () => {
        h.load.mockReturnValue(new Promise<void>(() => {}));
        const { setRenderError } = renderPreview({ documentType: "docx" });
        await waitFor(() => expect(h.docxCtor).toHaveBeenCalled());
        const opts = h.docxCtor.mock.calls[0][1] as { onError?: (e: unknown) => void };
        await act(async () => {
            opts.onError?.(new Error("engine boom"));
        });
        expect(setRenderError).toHaveBeenCalledWith("engine boom");
        expect(h.destroy).toHaveBeenCalled();
    });

    it("ignores a late engine onError after a successful load (no error painted over a working doc)", async () => {
        const { setRenderError } = renderPreview({ documentType: "docx" });
        await waitFor(() => expect(h.load).toHaveBeenCalled());
        await waitFor(() => expect(setRenderError).toHaveBeenLastCalledWith(null));
        const opts = h.docxCtor.mock.calls[0][1] as { onError?: (e: unknown) => void };
        opts.onError?.(new Error("late glitch"));
        expect(setRenderError).toHaveBeenLastCalledWith(null);
    });
});

describe("withTimeout", () => {
    afterEach(() => vi.useRealTimers());

    it("passes the resolved value through when it settles before the deadline", async () => {
        await expect(withTimeout(Promise.resolve("ok"), 1000, "boom")).resolves.toBe("ok");
    });

    it("rejects with the message when the promise never settles", async () => {
        vi.useFakeTimers();
        const rejection = expect(withTimeout(new Promise<void>(() => {}), 30_000, "Preview timed out while rendering.")).rejects.toThrow("Preview timed out while rendering.");
        await vi.advanceTimersByTimeAsync(30_000);
        await rejection;
    });

    it("consumes and logs a loser that rejects after the timer already won (no unhandled rejection)", async () => {
        vi.useFakeTimers();
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
        try {
            let rejectLoser: (e: unknown) => void = () => {};
            const loser = new Promise<void>((_, rej) => {
                rejectLoser = rej;
            });
            const rejection = expect(withTimeout(loser, 1000, "timed out")).rejects.toThrow("timed out");
            await vi.advanceTimersByTimeAsync(1000);
            await rejection;

            rejectLoser(new Error("late poison"));
            await Promise.resolve();
            expect(warn).toHaveBeenCalledWith("[ooxml] load rejected after timeout", expect.any(Error));
        } finally {
            warn.mockRestore();
            vi.useRealTimers();
        }
    });
});
