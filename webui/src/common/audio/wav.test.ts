// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, it, vi } from "vitest";

import { buildSttFormData, convertToWav16kMono, downmixToMono, encodeWav, encodeWavBytes, getFileExtension, resampleLinear, STT_SAMPLE_RATE } from "./wav";

const ascii = (view: DataView, offset: number, length: number): string => {
    let out = "";
    for (let i = 0; i < length; i++) {
        out += String.fromCharCode(view.getUint8(offset + i));
    }
    return out;
};

const fakeAudioBuffer = (samples: Float32Array, sampleRate = STT_SAMPLE_RATE): AudioBuffer =>
    ({
        numberOfChannels: 1,
        length: samples.length,
        sampleRate,
        duration: samples.length / sampleRate,
        getChannelData: () => samples,
    }) as unknown as AudioBuffer;

const stubAudioContext = (buffer: AudioBuffer) => {
    const close = vi.fn(async () => {});
    vi.stubGlobal(
        "AudioContext",
        class {
            decodeAudioData = vi.fn(async () => buffer);
            close = close;
        }
    );
    return { close };
};

const fakeBlob = (type: string): Blob => ({ type, size: 1024, arrayBuffer: async () => new ArrayBuffer(8) }) as unknown as Blob;

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe("encodeWavBytes", () => {
    it("produces a well-formed 16-bit mono PCM RIFF header", () => {
        const samples = new Float32Array([0, 0.5, -0.5, 1, -1]);
        const buffer = encodeWavBytes(samples, STT_SAMPLE_RATE);
        const view = new DataView(buffer);
        const dataLen = samples.length * 2;

        expect(ascii(view, 0, 4)).toBe("RIFF");
        expect(view.getUint32(4, true)).toBe(36 + dataLen);
        expect(ascii(view, 8, 4)).toBe("WAVE");

        expect(ascii(view, 12, 4)).toBe("fmt ");
        expect(view.getUint32(16, true)).toBe(16);
        expect(view.getUint16(20, true)).toBe(1);
        expect(view.getUint16(22, true)).toBe(1);
        expect(view.getUint32(24, true)).toBe(STT_SAMPLE_RATE);
        expect(view.getUint32(28, true)).toBe(STT_SAMPLE_RATE * 2);
        expect(view.getUint16(32, true)).toBe(2);
        expect(view.getUint16(34, true)).toBe(16);

        expect(ascii(view, 36, 4)).toBe("data");
        expect(view.getUint32(40, true)).toBe(dataLen);

        expect(buffer.byteLength).toBe(44 + dataLen);
    });

    it("maps float samples to the full signed int16 range", () => {
        const samples = new Float32Array([0, 1, -1, 0.5]);
        const view = new DataView(encodeWavBytes(samples, STT_SAMPLE_RATE));

        expect(view.getInt16(44, true)).toBe(0);
        expect(view.getInt16(46, true)).toBe(0x7fff);
        expect(view.getInt16(48, true)).toBe(-0x8000);
        expect(view.getInt16(50, true)).toBe(Math.trunc(0.5 * 0x7fff));
    });

    it("clamps out-of-range samples instead of overflowing", () => {
        const samples = new Float32Array([2, -2]);
        const view = new DataView(encodeWavBytes(samples, STT_SAMPLE_RATE));

        expect(view.getInt16(44, true)).toBe(0x7fff);
        expect(view.getInt16(46, true)).toBe(-0x8000);
    });

    it("emits a header-only buffer for empty input", () => {
        const buffer = encodeWavBytes(new Float32Array(0), STT_SAMPLE_RATE);
        expect(buffer.byteLength).toBe(44);

        const view = new DataView(buffer);
        expect(ascii(view, 0, 4)).toBe("RIFF");
        expect(view.getUint32(4, true)).toBe(36);
        expect(view.getUint32(40, true)).toBe(0);
    });
});

describe("encodeWav", () => {
    it("wraps the bytes in an audio/wav Blob", () => {
        const blob = encodeWav(new Float32Array([0, 0.25, -0.25]), STT_SAMPLE_RATE);
        expect(blob.type).toBe("audio/wav");
        expect(blob.size).toBe(44 + 3 * 2);
    });
});

describe("getFileExtension", () => {
    it("maps known recorder MIME types to extensions", () => {
        expect(getFileExtension("audio/mp4")).toBe("mp4");
        expect(getFileExtension("audio/webm;codecs=opus")).toBe("webm");
        expect(getFileExtension("audio/ogg")).toBe("ogg");
        expect(getFileExtension("audio/wav")).toBe("wav");
    });

    it("defaults to webm for unknown types", () => {
        expect(getFileExtension("audio/x-unknown")).toBe("webm");
    });
});

describe("downmixToMono", () => {
    it("returns channel 0 unchanged for mono input", () => {
        const samples = new Float32Array([0.1, -0.2, 0.3]);
        const buffer = fakeAudioBuffer(samples);
        expect(downmixToMono(buffer)).toBe(samples);
    });

    it("averages multiple channels", () => {
        const left = new Float32Array([1, 0, -1]);
        const right = new Float32Array([0, 0.5, 1]);
        const buffer = {
            numberOfChannels: 2,
            length: 3,
            sampleRate: STT_SAMPLE_RATE,
            getChannelData: (c: number) => (c === 0 ? left : right),
        } as unknown as AudioBuffer;
        const mono = downmixToMono(buffer);
        expect(Array.from(mono)).toEqual([0.5, 0.25, 0]);
    });
});

describe("resampleLinear", () => {
    it("returns input unchanged when rates match", () => {
        const samples = new Float32Array([0.1, 0.2]);
        expect(resampleLinear(samples, STT_SAMPLE_RATE, STT_SAMPLE_RATE)).toBe(samples);
    });

    it("downsamples by the rate ratio and interpolates", () => {
        const input = new Float32Array([0, 3, 6, 9, 12, 15]);
        const out = resampleLinear(input, 48000, 16000);
        expect(out.length).toBe(2);
        expect(out[0]).toBeCloseTo(0);
        expect(out[1]).toBeCloseTo(9);
    });

    it("linearly interpolates on a fractional ratio", () => {
        const out = resampleLinear(new Float32Array([0, 10, 20]), 24000, 16000);
        expect(out.length).toBe(2);
        expect(out[0]).toBeCloseTo(0);
        expect(out[1]).toBeCloseTo(15);
    });

    it("returns empty for empty input", () => {
        expect(resampleLinear(new Float32Array(0), 48000, 16000).length).toBe(0);
    });
});

describe("convertToWav16kMono", () => {
    it("decodes and re-encodes to an audio/wav blob", async () => {
        const { close } = stubAudioContext(fakeAudioBuffer(new Float32Array([0, 0.5, -0.5]), STT_SAMPLE_RATE));
        const out = await convertToWav16kMono(fakeBlob("audio/mp4"));
        expect(out.type).toBe("audio/wav");
        expect(out.size).toBe(44 + 3 * 2);
        expect(close).toHaveBeenCalledTimes(1);
    });

    it("rejects when the recording has no decodable audio", async () => {
        stubAudioContext(fakeAudioBuffer(new Float32Array(0)));
        await expect(convertToWav16kMono(fakeBlob("audio/mp4"))).rejects.toThrow(/no decodable audio/);
    });
});

describe("buildSttFormData", () => {
    it("uploads converted WAV and forwards provider/language", async () => {
        stubAudioContext(fakeAudioBuffer(new Float32Array([0, 0.5, -0.5])));
        const fd = await buildSttFormData(fakeBlob("audio/mp4"), "audio/mp4", { provider: "azure", language: "en-US" });

        const audio = fd.get("audio") as File;
        expect(audio.type).toBe("audio/wav");
        expect(audio.name).toBe("audio.wav");
        expect(fd.get("mimeType")).toBe("audio/wav");
        expect(fd.get("provider")).toBe("azure");
        expect(fd.get("language")).toBe("en-US");
    });

    it("omits the provider and language fields when not supplied", async () => {
        stubAudioContext(fakeAudioBuffer(new Float32Array([0, 0.5, -0.5])));
        const fd = await buildSttFormData(fakeBlob("audio/webm"), "audio/webm", { provider: "browser" });
        expect(fd.get("provider")).toBeNull();
        expect(fd.get("language")).toBeNull();
    });

    it("falls back to the raw recording when decoding is unavailable", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
        const raw = new Blob(["raw-audio-bytes"], { type: "audio/mp4" });
        const fd = await buildSttFormData(raw, "audio/mp4", {});

        const audio = fd.get("audio") as File;
        expect(audio.type).toBe("audio/mp4");
        expect(audio.name).toBe("audio.mp4");
        expect(fd.get("mimeType")).toBe("audio/mp4");
        expect(warn).toHaveBeenCalled();
    });
});
