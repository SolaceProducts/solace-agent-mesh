// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it, vi } from "vitest";

import { defaultSnippetFilename, nextUniqueFilename, nextUniqueSnippetFilename, type PastedTextItem, uploadPastedTextItems } from "./pasteUtils";

const item = (overrides: Partial<PastedTextItem> = {}): PastedTextItem => ({
    id: Math.random().toString(36).slice(2),
    content: "x",
    timestamp: 0,
    ...overrides,
});

const configured = (filename: string): PastedTextItem => item({ isConfigured: true, filename });
const pasted = (): PastedTextItem => item({ isConfigured: false });

describe("nextUniqueFilename", () => {
    it("returns the name unchanged when it is free", () => {
        expect(nextUniqueFilename("image.png", new Set())).toBe("image.png");
    });

    it("suffixes before the extension when the name is taken", () => {
        expect(nextUniqueFilename("image.png", new Set(["image.png"]))).toBe("image-2.png");
    });

    it("skips a contiguous run of taken names", () => {
        expect(nextUniqueFilename("image.png", new Set(["image.png", "image-2.png", "image-3.png"]))).toBe("image-4.png");
    });

    it("suffixes at the end when there is no extension", () => {
        expect(nextUniqueFilename("README", new Set(["README"]))).toBe("README-2");
    });

    it("treats a leading-dot name as all base and no extension", () => {
        expect(nextUniqueFilename(".gitignore", new Set([".gitignore"]))).toBe(".gitignore-2");
    });

    it("splits at the last dot for multi-dot names", () => {
        expect(nextUniqueFilename("archive.tar.gz", new Set(["archive.tar.gz"]))).toBe("archive.tar-2.gz");
    });
});

describe("nextUniqueSnippetFilename", () => {
    it("returns snippet.txt when nothing is taken", () => {
        expect(nextUniqueSnippetFilename(new Set())).toBe("snippet.txt");
    });

    it("falls back to snippet-2.txt when snippet.txt is taken", () => {
        expect(nextUniqueSnippetFilename(new Set(["snippet.txt"]))).toBe("snippet-2.txt");
    });

    it("skips a contiguous run of taken snippet names", () => {
        expect(nextUniqueSnippetFilename(new Set(["snippet.txt", "snippet-2.txt", "snippet-3.txt"]))).toBe("snippet-4.txt");
    });

    it("ignores gaps and returns the first free counter", () => {
        expect(nextUniqueSnippetFilename(new Set(["snippet.txt", "snippet-3.txt"]))).toBe("snippet-2.txt");
    });
});

describe("defaultSnippetFilename", () => {
    it("assigns successive snippet names to successive non-configured items", () => {
        const items = [pasted(), pasted(), pasted()];
        expect(defaultSnippetFilename(items, 0, [])).toBe("snippet.txt");
        expect(defaultSnippetFilename(items, 1, [])).toBe("snippet-2.txt");
        expect(defaultSnippetFilename(items, 2, [])).toBe("snippet-3.txt");
    });

    it("does not let earlier configured items consume snippet names", () => {
        const items = [configured("report.txt"), pasted()];
        expect(defaultSnippetFilename(items, 1, [])).toBe("snippet.txt");
    });

    it("skips a snippet name claimed by an earlier configured item", () => {
        const items = [configured("snippet.txt"), pasted()];
        expect(defaultSnippetFilename(items, 1, [])).toBe("snippet-2.txt");
    });

    it("accounts for both existing artifacts and earlier non-configured items", () => {
        const items = [pasted(), pasted()];
        expect(defaultSnippetFilename(items, 1, ["snippet.txt"])).toBe("snippet-3.txt");
    });
});

describe("uploadPastedTextItems", () => {
    const okUpload = () => vi.fn(async (file: File) => ({ uri: `mem://${file.name}`, sessionId: "s1" }));

    it("uploads non-configured items with successive snippet filenames", async () => {
        const upload = okUpload();
        const { uploaded, errors } = await uploadPastedTextItems([pasted(), pasted()], "s0", [], upload);

        expect(errors).toEqual([]);
        expect(uploaded.map(u => u.filename)).toEqual(["snippet.txt", "snippet-2.txt"]);
        expect(uploaded.every(u => u.mimeType === "text/plain")).toBe(true);
        expect(uploaded[0].uri).toBe("mem://snippet.txt");
    });

    it("uses a configured item's own filename, mimeType, and description", async () => {
        const upload = okUpload();
        const items = [configured("report.csv")];
        items[0].mimeType = "text/csv";
        items[0].description = "quarterly";

        const { uploaded } = await uploadPastedTextItems(items, "s0", [], upload);

        expect(uploaded[0]).toMatchObject({ filename: "report.csv", mimeType: "text/csv" });
        expect(upload).toHaveBeenCalledWith(expect.any(File), "s0", "quarterly", true);
    });

    it("collects an error from an {error} result and keeps going", async () => {
        const upload = vi.fn<(file: File) => Promise<{ uri: string; sessionId: string } | { error: string } | null>>().mockResolvedValueOnce({ error: "boom" }).mockResolvedValueOnce({ uri: "mem://snippet-2.txt", sessionId: "s1" });

        const { uploaded, errors } = await uploadPastedTextItems([pasted(), pasted()], "s0", [], upload);

        expect(errors).toEqual(["snippet.txt: boom"]);
        expect(uploaded.map(u => u.filename)).toEqual(["snippet-2.txt"]);
    });

    it("collects an error when the upload throws and keeps going", async () => {
        const upload = vi.fn<(file: File) => Promise<{ uri: string; sessionId: string } | { error: string } | null>>().mockRejectedValueOnce(new Error("network down")).mockResolvedValueOnce({ uri: "mem://snippet-2.txt", sessionId: "s1" });

        const { uploaded, errors } = await uploadPastedTextItems([pasted(), pasted()], "s0", [], upload);

        expect(errors).toEqual(["snippet.txt: network down"]);
        expect(uploaded).toHaveLength(1);
    });

    it("returns the backend-updated sessionId", async () => {
        const upload = vi.fn(async () => ({ uri: "mem://snippet.txt", sessionId: "server-session" }));
        const { sessionId } = await uploadPastedTextItems([pasted()], "", [], upload);
        expect(sessionId).toBe("server-session");
    });

    it("does no work and keeps the session for an empty item list", async () => {
        const upload = okUpload();
        const { uploaded, errors, sessionId } = await uploadPastedTextItems([], "s0", [], upload);
        expect(uploaded).toEqual([]);
        expect(errors).toEqual([]);
        expect(sessionId).toBe("s0");
        expect(upload).not.toHaveBeenCalled();
    });
});
