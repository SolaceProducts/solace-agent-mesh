// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export async function readAudioStream(response: Response): Promise<Uint8Array<ArrayBuffer>> {
    const reader = response.body?.getReader();
    if (!reader) {
        throw new Error("No response body reader available");
    }
    const chunks: Uint8Array[] = [];
    let totalSize = 0;
    for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        if (value && value.length > 0) {
            chunks.push(value);
            totalSize += value.length;
        }
    }
    if (totalSize === 0) {
        throw new Error("The speech service returned no audio");
    }
    const combined = new Uint8Array(totalSize);
    let offset = 0;
    for (const chunk of chunks) {
        combined.set(chunk, offset);
        offset += chunk.length;
    }
    return combined;
}

export function sniffAudioMime(bytes: Uint8Array): string {
    const isWav = bytes[0] === 0x52 && bytes[1] === 0x49 && bytes[2] === 0x46 && bytes[3] === 0x46;
    return isWav ? "audio/wav" : "audio/mpeg";
}
