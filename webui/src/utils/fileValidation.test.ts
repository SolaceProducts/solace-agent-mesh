// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { isZipFile, MAX_UPLOAD_SIZE_BYTES, validateFileSizes } from "./fileValidation";

const makeFile = (bytes: number[], name: string): File => new File([new Uint8Array(bytes)], name, { type: "application/octet-stream" });

describe("isZipFile", () => {
    it("returns true for a file with the PK\\x03\\x04 local-file-header signature", async () => {
        const zip = makeFile([0x50, 0x4b, 0x03, 0x04, 0x14, 0x00, 0x00, 0x00], "ok.zip");
        expect(await isZipFile(zip)).toBe(true);
    });

    it("returns false for a .doc renamed to .zip (OLE compound-file magic)", async () => {
        const fakeZip = makeFile([0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1], "report.zip");
        expect(await isZipFile(fakeZip)).toBe(false);
    });

    it("returns false for a PDF renamed to .zip", async () => {
        const fakeZip = makeFile([0x25, 0x50, 0x44, 0x46, 0x2d, 0x31, 0x2e, 0x34], "doc.zip");
        expect(await isZipFile(fakeZip)).toBe(false);
    });

    it("returns false for a file shorter than the 4-byte signature", async () => {
        const tiny = makeFile([0x50, 0x4b], "truncated.zip");
        expect(await isZipFile(tiny)).toBe(false);
    });

    it("returns false for an empty file", async () => {
        const empty = makeFile([], "empty.zip");
        expect(await isZipFile(empty)).toBe(false);
    });

    it("returns false for the empty-archive (PK\\x05\\x06) and spanned-archive (PK\\x07\\x08) variants", async () => {
        const emptyArchive = makeFile([0x50, 0x4b, 0x05, 0x06], "empty-archive.zip");
        const spannedArchive = makeFile([0x50, 0x4b, 0x07, 0x08], "spanned.zip");
        expect(await isZipFile(emptyArchive)).toBe(false);
        expect(await isZipFile(spannedArchive)).toBe(false);
    });
});

const fileOfSize = (size: number, name: string): File => {
    const file = new File(["x"], name);
    Object.defineProperty(file, "size", { value: size });
    return file;
};

describe("MAX_UPLOAD_SIZE_BYTES", () => {
    it("is 100 MB, mirroring the backend cap", () => {
        expect(MAX_UPLOAD_SIZE_BYTES).toBe(100 * 1024 * 1024);
    });

    it("rejects a file over the cap and accepts one at the cap", () => {
        const over = validateFileSizes([fileOfSize(MAX_UPLOAD_SIZE_BYTES + 1, "big.zip")], { maxSizeBytes: MAX_UPLOAD_SIZE_BYTES });
        expect(over.valid).toBe(false);

        const atCap = validateFileSizes([fileOfSize(MAX_UPLOAD_SIZE_BYTES, "ok.zip")], { maxSizeBytes: MAX_UPLOAD_SIZE_BYTES });
        expect(atCap.valid).toBe(true);
    });
});
