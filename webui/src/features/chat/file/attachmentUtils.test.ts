// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { getExtensionLabel, isImageType, MAX_THUMBNAIL_FILE_BYTES, mergeAttachments, supportsTextPreview } from "@/features/chat/file/attachmentUtils";

const file = (name: string, { body = "x", lastModified = 1000 }: { body?: string; lastModified?: number } = {}): File => new File([body], name, { type: "image/png", lastModified });

const names = (files: File[]): string[] => files.map(f => f.name);

describe("attachmentUtils — isImageType", () => {
    test("matches image/* mime types", () => {
        expect(isImageType("image/png", "a.png")).toBe(true);
        expect(isImageType("image/jpeg", "a.jpg")).toBe(true);
        expect(isImageType("image/svg+xml", "a.svg")).toBe(true);
    });

    test("matches known image extensions when mime is generic", () => {
        expect(isImageType("application/octet-stream", "photo.PNG")).toBe(true);
        expect(isImageType("application/octet-stream", "photo.webp")).toBe(true);
        expect(isImageType("application/octet-stream", "icon.ico")).toBe(true);
    });

    test("rejects non-image types", () => {
        expect(isImageType("text/plain", "a.txt")).toBe(false);
        expect(isImageType("application/pdf", "a.pdf")).toBe(false);
        expect(isImageType("application/zip", "a.zip")).toBe(false);
    });

    test("handles missing filename", () => {
        expect(isImageType("image/png")).toBe(true);
        expect(isImageType("application/octet-stream")).toBe(false);
    });
});

describe("attachmentUtils — supportsTextPreview", () => {
    test("matches text/* mime types", () => {
        expect(supportsTextPreview("text/plain")).toBe(true);
        expect(supportsTextPreview("text/markdown")).toBe(true);
        expect(supportsTextPreview("text/csv")).toBe(true);
    });

    test("matches structured-text mime keywords", () => {
        expect(supportsTextPreview("application/json")).toBe(true);
        expect(supportsTextPreview("application/xml")).toBe(true);
        expect(supportsTextPreview("application/javascript")).toBe(true);
        expect(supportsTextPreview("application/x-yaml")).toBe(true);
    });

    test("returns true for PPTX/DOCX mime types (caller must pre-filter)", () => {
        expect(supportsTextPreview("application/vnd.openxmlformats-officedocument.presentationml.presentation")).toBe(true);
        expect(supportsTextPreview("application/vnd.openxmlformats-officedocument.wordprocessingml.document")).toBe(true);
        expect(supportsTextPreview("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")).toBe(true);
    });

    test("matches known text extensions when mime is generic", () => {
        expect(supportsTextPreview("application/octet-stream", "notes.txt")).toBe(true);
        expect(supportsTextPreview("application/octet-stream", "script.py")).toBe(true);
        expect(supportsTextPreview("application/octet-stream", "config.TOML")).toBe(true);
    });

    test("rejects binary types with no matching extension", () => {
        expect(supportsTextPreview("application/pdf", "a.pdf")).toBe(false);
        expect(supportsTextPreview("image/png", "a.png")).toBe(false);
        expect(supportsTextPreview("application/zip", "a.zip")).toBe(false);
    });
});

describe("attachmentUtils — getExtensionLabel", () => {
    test("uppercases the extension", () => {
        expect(getExtensionLabel("photo.png")).toBe("PNG");
        expect(getExtensionLabel("README.md")).toBe("MD");
    });

    test("truncates extensions longer than 4 chars", () => {
        expect(getExtensionLabel("slides.pptx")).toBe("PPTX");
        expect(getExtensionLabel("bundle.tarball")).toBe("TARB");
    });

    test("falls back to FILE for extensionless names", () => {
        expect(getExtensionLabel("Makefile")).toBe("FILE");
    });
});

describe("attachmentUtils — MAX_THUMBNAIL_FILE_BYTES", () => {
    test("is 20 MB", () => {
        expect(MAX_THUMBNAIL_FILE_BYTES).toBe(20 * 1024 * 1024);
    });
});

describe("attachmentUtils — mergeAttachments", () => {
    test("keeps the first name and renames a later clash", () => {
        const first = mergeAttachments([], [file("image.png", { lastModified: 1000 })], []);
        const both = mergeAttachments(first, [file("image.png", { body: "different", lastModified: 2000 })], []);

        expect(names(both)).toEqual(["image.png", "image-2.png"]);
    });

    test("renames within a single batch", () => {
        const merged = mergeAttachments([], [file("shot.png"), file("shot.png"), file("shot.png")], []);

        expect(names(merged)).toEqual(["shot.png", "shot-2.png", "shot-3.png"]);
    });

    test("avoids the names of existing session artifacts", () => {
        const merged = mergeAttachments([], [file("image.png")], ["image.png", "image-2.png"]);

        expect(names(merged)).toEqual(["image-3.png"]);
    });

    test("suppresses an exact re-add", () => {
        const existing = [file("report.pdf", { lastModified: 500 })];
        const merged = mergeAttachments(existing, [file("report.pdf", { lastModified: 500 })], []);

        expect(merged).toBe(existing);
    });

    test("suppresses an exact re-add of a file that was renamed on its first add", () => {
        const first = mergeAttachments([], [file("report.pdf")], ["report.pdf"]);
        expect(names(first)).toEqual(["report-2.pdf"]);

        const second = mergeAttachments(first, [file("report.pdf")], ["report.pdf"]);

        expect(second).toBe(first);
    });

    test("still renames a genuinely different file with the same original name", () => {
        const first = mergeAttachments([], [file("report.pdf")], ["report.pdf"]);
        const second = mergeAttachments(first, [file("report.pdf", { body: "different", lastModified: 2000 })], ["report.pdf"]);

        expect(names(second)).toEqual(["report-2.pdf", "report-3.pdf"]);
    });

    test("dedupes against the original name after two successive renames", () => {
        const onceRenamed = mergeAttachments([], [file("report.pdf")], ["report.pdf"]);
        const twiceRenamed = mergeAttachments([], onceRenamed, ["report-2.pdf"]);
        expect(names(twiceRenamed)).toEqual(["report-2-2.pdf"]);

        const reAdded = mergeAttachments(twiceRenamed, [file("report.pdf")], []);

        expect(reAdded).toBe(twiceRenamed);
    });

    test("preserves the original File object when no rename is needed", () => {
        const original = file("unique.png");
        const merged = mergeAttachments([], [original], []);

        expect(merged[0]).toBe(original);
    });

    test("carries type, timestamp and content across a rename", () => {
        const incoming = file("image.png", { body: "second", lastModified: 2000 });
        const renamed = mergeAttachments([file("image.png")], [incoming], [])[1];

        expect(renamed.name).toBe("image-2.png");
        expect(renamed.type).toBe(incoming.type);
        expect(renamed.lastModified).toBe(incoming.lastModified);
        expect(renamed.size).toBe(incoming.size);
    });
});
