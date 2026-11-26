// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { evaluationKeys, EXAMPLE_ARTIFACT_GONE_MESSAGE } from "@/api/evaluations";

import { downloadBlob } from "@/utils/download";

import type { EvalArtifact } from "@/types/eval";

import { ArtifactPreviewView } from "@/features/evaluations/components/ArtifactPreviewView";

vi.mock("@/providers/contexts", async importActual => {
    const actual = await importActual<typeof import("@/providers/contexts")>();
    return {
        ...actual,
        useChatContext: () => ({ displayError: vi.fn() }),
        useChatMessageData: () => ({ setTaskIdInSidePanel: vi.fn(), setExpandedDocumentFilename: vi.fn(), openSidePanelTab: vi.fn() }),
    };
});

vi.mock("@/utils/download", () => ({ downloadBlob: vi.fn() }));

const artifact: EvalArtifact = { filename: "input.md", mimeType: "text/markdown", version: 0, size: 12 };

function textBlob(content: string, type = "text/markdown"): Blob {
    return Object.assign(new Blob([content], { type }), { text: () => Promise.resolve(content) });
}

function byteBlob(bytes: Uint8Array, type: string): Blob {
    const buffer = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
    return Object.assign(new Blob([buffer], { type }), { arrayBuffer: () => Promise.resolve(buffer) });
}

function renderPreview(fetchBlob: () => Promise<Blob>, cacheKey: readonly unknown[], subject: EvalArtifact = artifact) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: 0 } } });
    return render(
        <QueryClientProvider client={client}>
            <ArtifactPreviewView artifact={subject} fetchBlob={fetchBlob} cacheKey={cacheKey} onClose={vi.fn()} />
        </QueryClientProvider>
    );
}

describe("ArtifactPreviewView", () => {
    test("loads the bytes through the injected loader", async () => {
        const fetchBlob = vi.fn().mockResolvedValue(textBlob("# hello from the input file"));
        renderPreview(fetchBlob, evaluationKeys.runResultExampleArtifactBlob("r1", "input", "input.md", 0));

        await waitFor(() => expect(fetchBlob).toHaveBeenCalledTimes(1));

        expect(screen.getByText("input.md")).toBeTruthy();
        await waitFor(() => expect(screen.getByText("hello from the input file")).toBeTruthy());
        expect(screen.queryByText(/Failed to load/)).toBeNull();
    });

    test("shows the file-is-gone message inline when the load fails", async () => {
        const fetchBlob = vi.fn().mockRejectedValue(new Error(EXAMPLE_ARTIFACT_GONE_MESSAGE));
        renderPreview(fetchBlob, evaluationKeys.runResultExampleArtifactBlob("r1", "input", "input.md", 0));

        await waitFor(() => expect(screen.getByText(new RegExp(EXAMPLE_ARTIFACT_GONE_MESSAGE.slice(0, 40)))).toBeTruthy());
    });

    test("scopes the fetch to the supplied cache key", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: 0 } } });
        const inputFetch = vi.fn().mockResolvedValue(new Blob(["input bytes"]));
        const outputFetch = vi.fn().mockResolvedValue(new Blob(["output bytes"]));

        const { rerender } = render(
            <QueryClientProvider client={client}>
                <ArtifactPreviewView artifact={artifact} fetchBlob={inputFetch} cacheKey={["input", "input.md"]} onClose={vi.fn()} />
            </QueryClientProvider>
        );
        await waitFor(() => expect(inputFetch).toHaveBeenCalledTimes(1));

        rerender(
            <QueryClientProvider client={client}>
                <ArtifactPreviewView artifact={artifact} fetchBlob={outputFetch} cacheKey={["output", "input.md"]} onClose={vi.fn()} />
            </QueryClientProvider>
        );

        await waitFor(() => expect(outputFetch).toHaveBeenCalledTimes(1));
        expect(inputFetch).toHaveBeenCalledTimes(1);
    });

    test("downloads the bytes the open preview already holds", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: 0, staleTime: 1000 * 60 * 5 } } });
        const fetchBlob = vi.fn().mockResolvedValue(textBlob("# hello from the input file"));
        render(
            <QueryClientProvider client={client}>
                <ArtifactPreviewView artifact={artifact} fetchBlob={fetchBlob} cacheKey={["input", "input.md"]} onClose={vi.fn()} />
            </QueryClientProvider>
        );
        await waitFor(() => expect(fetchBlob).toHaveBeenCalledTimes(1));

        fireEvent.click(screen.getByRole("button", { name: /download/i }));

        await waitFor(() => expect(downloadBlob).toHaveBeenCalledTimes(1));
        expect(fetchBlob).toHaveBeenCalledTimes(1);
    });

    test("base64-encodes binary bytes for the image renderer", async () => {
        const png = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01, 0x02, 0xfd, 0xfe, 0xff]);
        const expected = btoa(String.fromCharCode(...png));
        const image: EvalArtifact = { filename: "shot.png", mimeType: "image/png", version: 0, size: png.length };

        renderPreview(() => Promise.resolve(byteBlob(png, "image/png")), ["evaluations", "blob", "r1", "shot.png", 0], image);

        const img = await waitFor(() => {
            const el = document.querySelector("img[src^='data:image/png;base64,']");
            expect(el).toBeTruthy();
            return el as HTMLImageElement;
        });
        expect(img.getAttribute("src")).toContain(expected);
    });

    test("encodes a payload spanning multiple chunks without dropping bytes", async () => {
        const bytes = new Uint8Array(0x8000 + 500).map((_, i) => i % 251);
        const expected = btoa(Array.from(bytes, b => String.fromCharCode(b)).join(""));
        const image: EvalArtifact = { filename: "big.png", mimeType: "image/png", version: 0, size: bytes.length };

        renderPreview(() => Promise.resolve(byteBlob(bytes, "image/png")), ["evaluations", "blob", "r1", "big.png", 0], image);

        const img = await waitFor(() => {
            const el = document.querySelector("img[src^='data:image/png;base64,']");
            expect(el).toBeTruthy();
            return el as HTMLImageElement;
        });
        expect(img.getAttribute("src")).toContain(expected);
    });

    test("surfaces a transform failure instead of spinning forever", async () => {
        const blob = Object.assign(new Blob(["# doc"], { type: "text/markdown" }), {
            text: () => Promise.reject(new Error("could not decode the artifact")),
        });

        renderPreview(() => Promise.resolve(blob), ["evaluations", "blob", "r1", "input.md", 0]);

        expect(await screen.findByText(/could not decode the artifact/)).toBeTruthy();
    });

    test("says so plainly, without fetching the bytes, for a file type it cannot render", async () => {
        const odd: EvalArtifact = { filename: "archive.tar.zst", mimeType: "application/zstd", version: 0, size: 10 };
        const fetchBlob = vi.fn().mockResolvedValue(new Blob(["x"]));

        renderPreview(fetchBlob, ["evaluations", "blob", "r1", "archive.tar.zst", 0], odd);

        expect(await screen.findByText(/No preview available for this file type/)).toBeTruthy();
        expect(fetchBlob).not.toHaveBeenCalled();
    });
});
