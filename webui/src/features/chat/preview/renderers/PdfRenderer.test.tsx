// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import PdfRenderer from "./PdfRenderer";

const { getDocument, workerOptions, pageScales } = vi.hoisted(() => ({
    getDocument: vi.fn(),
    workerOptions: { workerSrc: "" },
    pageScales: [] as number[],
}));

vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({ default: "/assets/pdf.worker.min-hash.mjs" }));
vi.mock("react-pdf", () => ({
    Document: ({ onLoadSuccess, children }: { onLoadSuccess?: (pdf: { numPages: number }) => void; children?: React.ReactNode }) => {
        queueMicrotask(() => onLoadSuccess?.({ numPages: 2 }));
        return <div data-testid="pdf-document">{children}</div>;
    },
    Page: ({ pageNumber, scale, onLoadSuccess }: { pageNumber: number; scale: number; onLoadSuccess?: (page: { width: number }) => void }) => {
        if (pageNumber === 1) {
            pageScales.push(scale);
            queueMicrotask(() => onLoadSuccess?.({ width: 600 }));
        }
        return <div data-testid="pdf-page" />;
    },
    pdfjs: { GlobalWorkerOptions: workerOptions, getDocument },
}));
vi.mock("@/api/artifacts/hooks", () => ({
    usePdfBlob: () => ({ data: "blob:http://localhost/pdf-1", error: null }),
}));

function pdfWithText(pages: string[][]) {
    return {
        destroy: vi.fn(),
        promise: Promise.resolve({
            numPages: pages.length,
            getPage: async (index: number) => ({
                getTextContent: async () => ({ items: pages[index - 1].map(str => ({ str })) }),
            }),
        }),
    };
}

describe("PdfRenderer", () => {
    beforeEach(() => {
        getDocument.mockReset();
    });

    test("points the PDF.js worker at the bundled worker asset", () => {
        expect(workerOptions.workerSrc).toBe("/assets/pdf.worker.min-hash.mjs");
    });

    test("loads the document for citation page boundaries from the PDF blob URL", async () => {
        getDocument.mockReturnValue(pdfWithText([["abc"], ["defgh"]]));

        render(<PdfRenderer url="/api/artifacts/doc.pdf" filename="doc.pdf" citationMaps={[{ location: "physical_page_2", char_start: 3, char_end: 6 }]} />);

        await waitFor(() => expect(getDocument).toHaveBeenCalledWith("blob:http://localhost/pdf-1"));
    });

    describe("initial zoom", () => {
        let viewerWidth = 0;
        let resizeCallbacks: (() => void)[] = [];

        beforeEach(() => {
            viewerWidth = 0;
            resizeCallbacks = [];
            pageScales.length = 0;
            vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(() => viewerWidth);
            vi.stubGlobal(
                "ResizeObserver",
                class {
                    constructor(callback: () => void) {
                        resizeCallbacks.push(callback);
                    }
                    observe() {}
                    disconnect() {}
                }
            );
        });

        afterEach(() => {
            vi.restoreAllMocks();
            vi.unstubAllGlobals();
        });

        test("keeps the user's zoom when the viewer resizes afterwards", async () => {
            viewerWidth = 1240;
            const { getAllByRole } = render(<PdfRenderer url="/api/artifacts/doc.pdf" filename="doc.pdf" />);
            await waitFor(() => expect(pageScales.at(-1)).toBe(1));

            act(() => getAllByRole("button")[1].click());
            const zoomed = pageScales.at(-1);
            viewerWidth = 640;
            act(() => resizeCallbacks.forEach(callback => callback()));

            expect(pageScales.at(-1)).toBe(zoomed);
            expect(zoomed).not.toBe(1);
        });
    });
});
