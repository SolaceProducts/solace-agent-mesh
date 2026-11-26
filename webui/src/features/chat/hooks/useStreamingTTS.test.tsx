// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { SpeechSettings } from "@/providers/contexts/AudioSettingsContext";

const post = vi.fn();

vi.mock("@/api/client", () => ({
    api: { webui: { post: (...args: unknown[]) => post(...args) } },
}));

const settings = { voice: "", ttsProvider: "gemini", playbackRate: 1.0 } as unknown as SpeechSettings;

vi.mock("@/providers/contexts", () => ({
    useAudioSettings: () => ({ settings, onTTSStart: vi.fn(), onTTSEnd: vi.fn() }),
}));

import { useStreamingTTS } from "./useStreamingTTS";

const LONG_TEXT = "This is a sufficiently long sentence to be processed.";

const emptyStream = () =>
    ({
        ok: true,
        statusText: "OK",
        body: { getReader: () => ({ read: () => Promise.resolve({ done: true, value: undefined }) }) },
    }) as unknown as Response;

describe("useStreamingTTS error surfacing", () => {
    beforeEach(() => {
        post.mockReset();
    });
    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("calls onError when the stream yields no audio", async () => {
        post.mockImplementation((endpoint: string) => {
            if (endpoint === "/api/v1/speech/preprocess") return Promise.resolve({ text: LONG_TEXT });
            return Promise.resolve(emptyStream());
        });
        const onError = vi.fn();
        const { result } = renderHook(() => useStreamingTTS({ messageId: "m-1", onError }));

        await act(async () => {
            await result.current.processStreamingText(LONG_TEXT, true);
        });

        await waitFor(() => expect(onError).toHaveBeenCalledWith(expect.stringContaining("no audio")));
    });

    test("calls onError when the stream request fails", async () => {
        post.mockImplementation((endpoint: string) => {
            if (endpoint === "/api/v1/speech/preprocess") return Promise.resolve({ text: LONG_TEXT });
            return Promise.resolve({ ok: false, statusText: "Bad Gateway" } as Response);
        });
        const onError = vi.fn();
        const { result } = renderHook(() => useStreamingTTS({ messageId: "m-2", onError }));

        await act(async () => {
            await result.current.processStreamingText(LONG_TEXT, true);
        });

        await waitFor(() => expect(onError).toHaveBeenCalled());
    });
});
