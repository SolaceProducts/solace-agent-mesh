// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const STT_SAMPLE_RATE = 16000;

export function getFileExtension(mimeType: string): string {
    const mimeToExt: Record<string, string> = {
        "audio/webm": "webm",
        "audio/mp4": "mp4",
        "audio/ogg": "ogg",
        "audio/wav": "wav",
    };

    for (const [mime, ext] of Object.entries(mimeToExt)) {
        if (mimeType.includes(mime)) {
            return ext;
        }
    }

    return "webm";
}

export function downmixToMono(buffer: AudioBuffer): Float32Array {
    const channels = buffer.numberOfChannels;
    if (channels <= 1) {
        return buffer.getChannelData(0);
    }
    const length = buffer.length;
    const mono = new Float32Array(length);
    for (let c = 0; c < channels; c++) {
        const data = buffer.getChannelData(c);
        for (let i = 0; i < length; i++) {
            mono[i] += data[i] / channels;
        }
    }
    return mono;
}

export function resampleLinear(input: Float32Array, srcRate: number, dstRate: number): Float32Array {
    if (srcRate === dstRate || input.length === 0) {
        return input;
    }
    const ratio = srcRate / dstRate;
    const outLength = Math.floor(input.length / ratio);
    const out = new Float32Array(outLength);
    for (let i = 0; i < outLength; i++) {
        const pos = i * ratio;
        const i0 = Math.floor(pos);
        const i1 = Math.min(i0 + 1, input.length - 1);
        out[i] = input[i0] + (input[i1] - input[i0]) * (pos - i0);
    }
    return out;
}

export async function convertToWav16kMono(blob: Blob): Promise<Blob> {
    const AudioCtx = globalThis.AudioContext || (globalThis as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
    const ctx = new AudioCtx();
    try {
        const decoded = await ctx.decodeAudioData(await blob.arrayBuffer());
        const mono = downmixToMono(decoded);
        const resampled = resampleLinear(mono, decoded.sampleRate, STT_SAMPLE_RATE);
        if (resampled.length === 0) {
            throw new Error("Recording contained no decodable audio");
        }
        return encodeWav(resampled, STT_SAMPLE_RATE);
    } finally {
        void ctx.close().catch(() => {});
    }
}

export async function buildSttFormData(recordedBlob: Blob, recordedMimeType: string, options: { provider?: string; language?: string } = {}): Promise<FormData> {
    let audioBlob = recordedBlob;
    try {
        audioBlob = await convertToWav16kMono(recordedBlob);
    } catch (error_) {
        console.warn("STT WAV conversion failed; uploading raw recording", {
            mimeType: recordedMimeType,
            blobSize: recordedBlob.size,
            userAgent: globalThis.navigator?.userAgent,
            error: error_,
        });
    }

    const isWav = audioBlob.type === "audio/wav";
    const fileName = isWav ? "audio.wav" : `audio.${getFileExtension(recordedMimeType)}`;

    const formData = new FormData();
    formData.append("audio", audioBlob, fileName);
    formData.append("mimeType", audioBlob.type);
    if (options.provider && options.provider !== "browser") {
        formData.append("provider", options.provider);
    }
    if (options.language) {
        formData.append("language", options.language);
    }
    return formData;
}

export function encodeWavBytes(samples: Float32Array, sampleRate: number): ArrayBuffer {
    const bytesPerSample = 2;
    const dataLen = samples.length * bytesPerSample;
    const buffer = new ArrayBuffer(44 + dataLen);
    const view = new DataView(buffer);

    const writeString = (offset: number, str: string) => {
        for (let i = 0; i < str.length; i++) {
            view.setUint8(offset + i, str.codePointAt(i) ?? 0);
        }
    };

    writeString(0, "RIFF");
    view.setUint32(4, 36 + dataLen, true);
    writeString(8, "WAVE");
    writeString(12, "fmt ");
    view.setUint32(16, 16, true);
    view.setUint16(20, 1, true);
    view.setUint16(22, 1, true);
    view.setUint32(24, sampleRate, true);
    view.setUint32(28, sampleRate * bytesPerSample, true);
    view.setUint16(32, bytesPerSample, true);
    view.setUint16(34, 16, true);
    writeString(36, "data");
    view.setUint32(40, dataLen, true);

    let offset = 44;
    for (const sample of samples) {
        const s = Math.max(-1, Math.min(1, sample));
        view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7fff, true);
        offset += bytesPerSample;
    }

    return buffer;
}

export function encodeWav(samples: Float32Array, sampleRate: number): Blob {
    return new Blob([encodeWavBytes(samples, sampleRate)], { type: "audio/wav" });
}
