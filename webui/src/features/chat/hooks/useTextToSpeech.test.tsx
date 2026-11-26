// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { SpeechSettings } from "@/providers/contexts/AudioSettingsContext";

const post = vi.fn();
const get = vi.fn();

vi.mock("@/api/client", () => ({
    api: { webui: { post: (...args: unknown[]) => post(...args), get: (...args: unknown[]) => get(...args) } },
}));

const externalSettings = {
    engineTTS: "external",
    ttsProvider: "gemini",
    voice: "",
    playbackRate: 1.0,
    cacheTTS: false,
    languageSTT: "en-US",
} as unknown as SpeechSettings;

vi.mock("@/providers/contexts", () => ({
    useAudioSettings: () => ({ settings: externalSettings }),
}));

import { useTextToSpeech } from "./useTextToSpeech";

const emptyStreamResponse = () =>
    ({
        ok: true,
        statusText: "OK",
        body: { getReader: () => ({ read: () => Promise.resolve({ done: true, value: undefined }) }) },
    }) as unknown as Response;

describe("useTextToSpeech external mode", () => {
    beforeEach(() => {
        post.mockReset();
        get.mockReset();
        get.mockResolvedValue({ voices: [], default: "" });
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("clears isLoading and reports an error when the stream yields no audio", async () => {
        post.mockImplementation((endpoint: string) => {
            if (endpoint === "/api/v1/speech/preprocess") {
                return Promise.resolve({ text: "hello" });
            }
            return Promise.resolve(emptyStreamResponse());
        });

        const onError = vi.fn();
        const { result } = renderHook(() => useTextToSpeech({ onError }));

        await act(async () => {
            await result.current.speak("hello");
        });

        await waitFor(() => expect(result.current.isLoading).toBe(false));
        expect(result.current.isSpeaking).toBe(false);
        expect(onError).toHaveBeenCalledWith(expect.stringContaining("no audio"));
    });

    test("clears isLoading when the stream request fails", async () => {
        post.mockImplementation((endpoint: string) => {
            if (endpoint === "/api/v1/speech/preprocess") {
                return Promise.resolve({ text: "hello" });
            }
            return Promise.resolve({ ok: false, statusText: "Bad Gateway" } as Response);
        });

        const onError = vi.fn();
        const { result } = renderHook(() => useTextToSpeech({ onError }));

        await act(async () => {
            await result.current.speak("hello");
        });

        await waitFor(() => expect(result.current.isLoading).toBe(false));
        expect(onError).toHaveBeenCalled();
    });
});

const streamResponse = (bytes: Uint8Array) => {
    let sent = false;
    return {
        ok: true,
        statusText: "OK",
        body: {
            getReader: () => ({
                read: () => (sent ? Promise.resolve({ done: true, value: undefined }) : ((sent = true), Promise.resolve({ done: false, value: bytes }))),
            }),
        },
    } as unknown as Response;
};

const WAV_BYTES = new Uint8Array([0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00]);
const MP3_BYTES = new Uint8Array([0xff, 0xfb, 0x90, 0x00]);

interface FakeAudioEl {
    onplay: (() => void) | null;
    onended: (() => void) | null;
    onerror: (() => void) | null;
    src: string;
    playbackRate: number;
    play(): Promise<void>;
    pause(): void;
}

describe("useTextToSpeech external playback", () => {
    let createObjectURL: ReturnType<typeof vi.fn>;
    let revokeObjectURL: ReturnType<typeof vi.fn>;
    let plays: number;
    let lastAudio: FakeAudioEl | null;
    let playBehavior: "resolve" | "notallowed";
    let cacheStore: Map<string, Response>;
    let cachePut: ReturnType<typeof vi.fn>;
    let origCreate: typeof URL.createObjectURL;
    let origRevoke: typeof URL.revokeObjectURL;

    beforeEach(() => {
        post.mockReset();
        get.mockReset();
        get.mockResolvedValue({ voices: [], default: "" });
        externalSettings.cacheTTS = false;
        externalSettings.ttsProvider = "gemini";

        plays = 0;
        lastAudio = null;
        playBehavior = "resolve";
        const capture = (a: FakeAudioEl) => {
            lastAudio = a;
        };
        class FakeAudio implements FakeAudioEl {
            onplay: (() => void) | null = null;
            onended: (() => void) | null = null;
            onerror: (() => void) | null = null;
            src = "";
            playbackRate = 1;
            constructor(src?: string) {
                if (src) this.src = src;
                capture(this);
            }
            play() {
                plays++;
                if (playBehavior === "notallowed") {
                    return Promise.reject(Object.assign(new Error("blocked"), { name: "NotAllowedError" }));
                }
                this.onplay?.();
                return Promise.resolve();
            }
            pause() {}
        }
        vi.stubGlobal("Audio", FakeAudio as unknown as typeof Audio);

        cacheStore = new Map();
        cachePut = vi.fn(async (k: string, r: Response) => {
            cacheStore.set(k, r);
        });
        vi.stubGlobal("caches", {
            open: async () => ({ match: async (k: string) => cacheStore.get(k), put: cachePut }),
        });

        origCreate = URL.createObjectURL;
        origRevoke = URL.revokeObjectURL;
        createObjectURL = vi.fn(() => "blob:fake");
        revokeObjectURL = vi.fn();
        URL.createObjectURL = createObjectURL as unknown as typeof URL.createObjectURL;
        URL.revokeObjectURL = revokeObjectURL as unknown as typeof URL.revokeObjectURL;
    });

    afterEach(() => {
        URL.createObjectURL = origCreate;
        URL.revokeObjectURL = origRevoke;
        externalSettings.cacheTTS = false;
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    test("plays one element, sniffs RIFF as audio/wav, and sends no runId", async () => {
        post.mockResolvedValue(streamResponse(WAV_BYTES));
        const onStart = vi.fn();
        const onEnd = vi.fn();
        const { result } = renderHook(() => useTextToSpeech({ onStart, onEnd }));

        await act(async () => {
            await result.current.speak("hello");
        });

        await waitFor(() => expect(result.current.isSpeaking).toBe(true));
        expect(result.current.isLoading).toBe(false);
        expect(plays).toBe(1);
        expect(onStart).toHaveBeenCalled();

        const blob = createObjectURL.mock.calls[0][0] as Blob;
        expect(blob.type).toBe("audio/wav");

        const streamCall = post.mock.calls.find(c => c[0] === "/api/v1/speech/tts/stream");
        expect(streamCall).toBeTruthy();
        expect(streamCall?.[1]).not.toHaveProperty("runId");

        act(() => lastAudio?.onended?.());
        expect(onEnd).toHaveBeenCalled();
        expect(result.current.isSpeaking).toBe(false);
        expect(revokeObjectURL).toHaveBeenCalled();
    });

    test("sniffs non-RIFF audio as audio/mpeg", async () => {
        post.mockResolvedValue(streamResponse(MP3_BYTES));
        const { result } = renderHook(() => useTextToSpeech({}));

        await act(async () => {
            await result.current.speak("hello");
        });

        await waitFor(() => expect(result.current.isSpeaking).toBe(true));
        const blob = createObjectURL.mock.calls[0][0] as Blob;
        expect(blob.type).toBe("audio/mpeg");
    });

    test("reports an error and clears loading when playback errors", async () => {
        post.mockResolvedValue(streamResponse(WAV_BYTES));
        const onError = vi.fn();
        const { result } = renderHook(() => useTextToSpeech({ onError }));

        await act(async () => {
            await result.current.speak("hello");
        });
        await waitFor(() => expect(result.current.isSpeaking).toBe(true));

        act(() => lastAudio?.onerror?.());
        expect(onError).toHaveBeenCalledWith("Failed to play audio");
        expect(result.current.isSpeaking).toBe(false);
    });

    test("surfaces the autoplay-policy hint when play() is blocked", async () => {
        post.mockResolvedValue(streamResponse(WAV_BYTES));
        playBehavior = "notallowed";
        const onError = vi.fn();
        const { result } = renderHook(() => useTextToSpeech({ onError }));

        await act(async () => {
            await result.current.speak("hello");
        });

        await waitFor(() => expect(result.current.isLoading).toBe(false));
        expect(onError).toHaveBeenCalledWith(expect.stringContaining("Click the speaker button again"));
    });

    test("plays from cache without hitting the stream endpoint", async () => {
        externalSettings.cacheTTS = true;
        const key = `hello--gemini`;
        cacheStore.set(key, new Response(new Blob([WAV_BYTES], { type: "audio/wav" })));
        const { result } = renderHook(() => useTextToSpeech({}));

        await act(async () => {
            await result.current.speak("hello");
        });

        await waitFor(() => expect(result.current.isSpeaking).toBe(true));
        expect(plays).toBe(1);
        expect(post.mock.calls.some(c => c[0] === "/api/v1/speech/tts/stream")).toBe(false);
    });

    test("caches the synthesized audio on a cache miss", async () => {
        externalSettings.cacheTTS = true;
        post.mockResolvedValue(streamResponse(WAV_BYTES));
        const { result } = renderHook(() => useTextToSpeech({}));

        await act(async () => {
            await result.current.speak("hello");
        });

        await waitFor(() => expect(result.current.isSpeaking).toBe(true));
        expect(cachePut).toHaveBeenCalled();
        expect(cacheStore.has(`hello--gemini`)).toBe(true);
    });

    test("keys the cache by provider (no cross-provider replay)", async () => {
        externalSettings.cacheTTS = true;
        externalSettings.ttsProvider = "azure";
        cacheStore.set(`hello--gemini`, new Response(new Blob([WAV_BYTES], { type: "audio/wav" })));
        post.mockResolvedValue(streamResponse(WAV_BYTES));
        const { result } = renderHook(() => useTextToSpeech({}));

        await act(async () => {
            await result.current.speak("hello");
        });

        await waitFor(() => expect(result.current.isSpeaking).toBe(true));
        expect(post.mock.calls.some(c => c[0] === "/api/v1/speech/tts/stream")).toBe(true);
        expect(cacheStore.has(`hello--azure`)).toBe(true);
    });

    test("still plays when the cache is unavailable", async () => {
        externalSettings.cacheTTS = true;
        vi.stubGlobal("caches", {
            open: async () => {
                throw new Error("caches unavailable");
            },
        });
        post.mockResolvedValue(streamResponse(WAV_BYTES));
        const { result } = renderHook(() => useTextToSpeech({}));

        await act(async () => {
            await result.current.speak("hello");
        });

        await waitFor(() => expect(result.current.isSpeaking).toBe(true));
        expect(plays).toBe(1);
    });
});
