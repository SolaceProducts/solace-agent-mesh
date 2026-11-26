// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DocumentThumbnail, supportsThumbnail } from "@/features/chat/file/DocumentThumbnail";

import { StoryProvider } from "@/testing/providers/StoryProvider";

vi.mock("react-pdf", () => ({
    Document: ({ children }: { children: React.ReactNode }) => <div data-testid="pdf-document">{children}</div>,
    Page: () => <div data-testid="pdf-page" />,
    pdfjs: { GlobalWorkerOptions: { workerSrc: "" } },
}));

const h = vi.hoisted(() => ({
    docxLoad: vi.fn(),
    pptxLoad: vi.fn(),
    xlsxLoad: vi.fn(),
    fakeBitmap: () => ({ width: 160, height: 208, close: vi.fn() }),
}));
vi.mock("@silurus/ooxml/docx", () => ({ DocxDocument: { load: h.docxLoad } }));
vi.mock("@silurus/ooxml/pptx", () => ({ PptxPresentation: { load: h.pptxLoad } }));
vi.mock("@silurus/ooxml/xlsx", () => ({ XlsxWorkbook: { load: h.xlsxLoad } }));

const uniqueB64 = () => btoa(`doc-${Math.random()}-${Date.now()}`);

describe("supportsThumbnail", () => {
    it("returns true for PDFs (extension and mime type)", () => {
        expect(supportsThumbnail("report.pdf")).toBe(true);
        expect(supportsThumbnail("REPORT.PDF")).toBe(true);
        expect(supportsThumbnail("report", "application/pdf")).toBe(true);
    });

    it("returns true for OOXML office documents (extension and mime type)", () => {
        expect(supportsThumbnail("doc.docx")).toBe(true);
        expect(supportsThumbnail("slides.pptx")).toBe(true);
        expect(supportsThumbnail("sheet.xlsx")).toBe(true);
        expect(supportsThumbnail("doc", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")).toBe(true);
        expect(supportsThumbnail("slides", "application/vnd.openxmlformats-officedocument.presentationml.presentation")).toBe(true);
    });

    it("returns false for legacy binary and OpenDocument formats (WASM engine is OOXML-only)", () => {
        expect(supportsThumbnail("legacy.doc")).toBe(false);
        expect(supportsThumbnail("legacy.ppt")).toBe(false);
        expect(supportsThumbnail("legacy.xls")).toBe(false);
        expect(supportsThumbnail("opendoc.odt")).toBe(false);
        expect(supportsThumbnail("openpres.odp")).toBe(false);
        expect(supportsThumbnail("opensheet.ods")).toBe(false);
        expect(supportsThumbnail("legacy", "application/msword")).toBe(false);
    });

    it("returns false for unsupported formats", () => {
        expect(supportsThumbnail("photo.jpg")).toBe(false);
        expect(supportsThumbnail("photo.png", "image/png")).toBe(false);
        expect(supportsThumbnail("data.json", "application/json")).toBe(false);
        expect(supportsThumbnail("noextension")).toBe(false);
        expect(supportsThumbnail("file", "text/plain")).toBe(false);
    });
});

function renderThumbnail(props: Partial<React.ComponentProps<typeof DocumentThumbnail>> = {}, flagOverrides: Record<string, boolean> = {}) {
    const defaultProps: React.ComponentProps<typeof DocumentThumbnail> = {
        content: uniqueB64(),
        filename: "doc.docx",
        mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    };
    return render(
        <MemoryRouter>
            <StoryProvider flagValues={{ binary_artifact_preview: true, ...flagOverrides }}>
                <DocumentThumbnail {...defaultProps} {...props} />
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("DocumentThumbnail rendering routing", () => {
    beforeEach(() => {
        h.docxLoad.mockResolvedValue({ renderPageToBitmap: vi.fn().mockResolvedValue(h.fakeBitmap()), destroy: vi.fn() });
        h.pptxLoad.mockResolvedValue({ renderSlideToBitmap: vi.fn().mockResolvedValue(h.fakeBitmap()), destroy: vi.fn() });
        h.xlsxLoad.mockResolvedValue({ renderViewportToBitmap: vi.fn().mockResolvedValue(h.fakeBitmap()), destroy: vi.fn() });
        vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ drawImage: vi.fn() } as unknown as CanvasRenderingContext2D);
        vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockReturnValue("data:image/png;base64,AAAA");
    });

    afterEach(() => {
        vi.restoreAllMocks();
        h.docxLoad.mockReset();
        h.pptxLoad.mockReset();
        h.xlsxLoad.mockReset();
    });

    it("renders a DOCX via the docx WASM engine and shows an image", async () => {
        const onLoad = vi.fn();
        const { container } = renderThumbnail({ filename: "report.docx", onLoad });
        await waitFor(() => expect(h.docxLoad).toHaveBeenCalled());
        await waitFor(() => expect(container.querySelector("img")).not.toBeNull());
        expect(onLoad).toHaveBeenCalled();
    });

    it("routes PPTX to the pptx engine and XLSX to the xlsx engine", async () => {
        renderThumbnail({ filename: "deck.pptx", mimeType: "application/vnd.openxmlformats-officedocument.presentationml.presentation" });
        await waitFor(() => expect(h.pptxLoad).toHaveBeenCalled());

        renderThumbnail({ filename: "book.xlsx", mimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" });
        await waitFor(() => expect(h.xlsxLoad).toHaveBeenCalled());
    });

    it("renders PDFs via react-pdf without touching the WASM engines", async () => {
        const { findByTestId } = renderThumbnail({ filename: "report.pdf", mimeType: "application/pdf" });
        expect(await findByTestId("pdf-document")).toBeTruthy();
        expect(h.docxLoad).not.toHaveBeenCalled();
    });

    it("does not render and fires onError when the feature is disabled", async () => {
        const onError = vi.fn();
        renderThumbnail({ filename: "report.docx", onError }, { binary_artifact_preview: false });
        await waitFor(() => expect(onError).toHaveBeenCalled());
        expect(h.docxLoad).not.toHaveBeenCalled();
    });

    it("does not render legacy/unsupported formats and fires onError", async () => {
        const onError = vi.fn();
        renderThumbnail({ filename: "photo.jpg", mimeType: "image/jpeg", onError });
        await waitFor(() => expect(onError).toHaveBeenCalled());
        expect(h.docxLoad).not.toHaveBeenCalled();
    });

    it("fires onError when content is empty", async () => {
        const onError = vi.fn();
        renderThumbnail({ content: "", filename: "report.docx", onError });
        await waitFor(() => expect(onError).toHaveBeenCalled());
    });

    it("degrades gracefully (onError, no image) when the engine fails to parse", async () => {
        h.docxLoad.mockReset();
        h.docxLoad.mockRejectedValueOnce(new Error("bad zip"));
        const onError = vi.fn();
        const { container } = renderThumbnail({ filename: "corrupt.docx", onError });
        await waitFor(() => expect(onError).toHaveBeenCalled());
        expect(container.querySelector("img")).toBeNull();
    });

    it("serves a cached PNG on the second render of the same content without re-parsing", async () => {
        const content = btoa("cache-hit-fixture-docx");
        const first = renderThumbnail({ content, filename: "cached.docx" });
        await waitFor(() => expect(first.container.querySelector("img")).not.toBeNull());
        expect(h.docxLoad).toHaveBeenCalledTimes(1);
        first.unmount();

        const onLoad = vi.fn();
        const second = renderThumbnail({ content, filename: "cached.docx", onLoad });
        await waitFor(() => expect(second.container.querySelector("img")).not.toBeNull());
        expect(h.docxLoad).toHaveBeenCalledTimes(1);
        expect(onLoad).toHaveBeenCalled();
    });

    it("fires onError without touching the engine when content is not valid base64", async () => {
        const onError = vi.fn();
        const { container } = renderThumbnail({ content: "@@@not-base64@@@", filename: "broken.docx", onError });
        await waitFor(() => expect(onError).toHaveBeenCalled());
        expect(container.querySelector("img")).toBeNull();
        expect(h.docxLoad).not.toHaveBeenCalled();
    });

    it("gives up (onError, no image) when the engine never settles past the timeout", async () => {
        vi.useFakeTimers();
        try {
            h.docxLoad.mockReset();
            h.docxLoad.mockReturnValue(new Promise(() => {}));
            const onError = vi.fn();
            const { container } = renderThumbnail({ filename: "hang.docx", onError });
            await act(async () => {
                await vi.advanceTimersByTimeAsync(30_000);
            });
            expect(onError).toHaveBeenCalled();
            expect(container.querySelector("img")).toBeNull();
        } finally {
            vi.useRealTimers();
        }
    });
});
