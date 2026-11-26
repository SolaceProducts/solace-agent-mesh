// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { readAudioStream, sniffAudioMime } from "./ttsStream";

const streamOf = (...chunks: Uint8Array[]): Response => {
    let i = 0;
    return {
        body: {
            getReader: () => ({
                read: () => (i < chunks.length ? Promise.resolve({ done: false, value: chunks[i++] }) : Promise.resolve({ done: true, value: undefined })),
            }),
        },
    } as unknown as Response;
};

describe("readAudioStream", () => {
    test("reassembles a payload split across multiple reads", async () => {
        const out = await readAudioStream(streamOf(new Uint8Array([0x52, 0x49, 0x46, 0x46]), new Uint8Array([1, 2, 3]), new Uint8Array([4, 5])));
        expect(Array.from(out)).toEqual([0x52, 0x49, 0x46, 0x46, 1, 2, 3, 4, 5]);
    });

    test("throws when the stream yields no bytes", async () => {
        await expect(readAudioStream(streamOf())).rejects.toThrow(/no audio/i);
    });

    test("throws when the response has no readable body", async () => {
        await expect(readAudioStream({} as Response)).rejects.toThrow(/no response body/i);
    });
});

describe("sniffAudioMime", () => {
    test("detects RIFF as audio/wav", () => {
        expect(sniffAudioMime(new Uint8Array([0x52, 0x49, 0x46, 0x46, 0x00]))).toBe("audio/wav");
    });

    test("falls back to audio/mpeg for non-RIFF bytes", () => {
        expect(sniffAudioMime(new Uint8Array([0xff, 0xfb, 0x00, 0x00]))).toBe("audio/mpeg");
    });
});
