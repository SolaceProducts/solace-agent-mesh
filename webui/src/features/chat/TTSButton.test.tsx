// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { agentUIMode, FULL_UI_MODE } from "@/providers/contexts";
import type { AudioSettingsContextValue, SpeechSettings } from "@/providers/contexts/AudioSettingsContext";
import type { UIMode } from "@/providers/contexts/UIModeContext";

import type { MessageFE } from "@/types/chat";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { TTSButton } from "./TTSButton";

const { processStreamingText, speak } = vi.hoisted(() => ({ processStreamingText: vi.fn(), speak: vi.fn() }));
vi.mock("@/features/chat/hooks", () => ({
    useStreamingTTS: () => ({ isPlaying: false, isLoading: false, processStreamingText, stop: vi.fn() }),
    useTextToSpeech: () => ({ isSpeaking: false, isLoading: false, speak, stop: vi.fn() }),
}));

const TTS_LABEL = "Read aloud";

const message: MessageFE = {
    isUser: false,
    isComplete: true,
    parts: [{ kind: "text", text: "Hello there, this is an assistant reply." }],
    metadata: { messageId: "m-1" },
} as unknown as MessageFE;

const ttsOnSettings: Partial<AudioSettingsContextValue> = {
    settings: { textToSpeech: true } as SpeechSettings,
};

const renderButton = (uiMode?: UIMode) =>
    render(
        <StoryProvider configContextValues={{ textToSpeechEnabled: true }} audioSettingsContextValues={ttsOnSettings} uiModeContextValues={uiMode}>
            <TTSButton message={message} />
        </StoryProvider>
    );

describe("TTSButton gating by UI mode", () => {
    test("renders the TTS button when enabled in full mode", () => {
        renderButton();
        expect(screen.getByLabelText(TTS_LABEL)).toBeInTheDocument();
    });

    test("renders nothing in agent mode even when TTS is enabled in config", () => {
        renderButton(agentUIMode("OrchestratorAgent"));
        expect(screen.queryByLabelText(TTS_LABEL)).not.toBeInTheDocument();
    });

    test("renders nothing when only allowSpeech is false", () => {
        renderButton({ ...FULL_UI_MODE, allowSpeech: false });
        expect(screen.queryByLabelText(TTS_LABEL)).not.toBeInTheDocument();
    });
});

describe("TTSButton auto-play gating by UI mode", () => {
    const streamingMessage: MessageFE = {
        isUser: false,
        isComplete: false,
        parts: [{ kind: "text", text: "This is a sufficiently long streaming assistant reply to trigger auto-play." }],
        metadata: { messageId: "m-stream" },
    } as unknown as MessageFE;

    const convoSettings: Partial<AudioSettingsContextValue> = {
        settings: { textToSpeech: true, conversationMode: true } as SpeechSettings,
    };

    beforeEach(() => {
        processStreamingText.mockClear();
        speak.mockClear();
    });

    test("auto-plays streaming TTS in full mode when conversationMode is on", () => {
        render(
            <StoryProvider configContextValues={{ textToSpeechEnabled: true }} audioSettingsContextValues={convoSettings}>
                <TTSButton message={streamingMessage} />
            </StoryProvider>
        );
        expect(processStreamingText).toHaveBeenCalled();
    });

    test("does not auto-play in agent mode even when conversationMode is on", () => {
        render(
            <StoryProvider configContextValues={{ textToSpeechEnabled: true }} audioSettingsContextValues={convoSettings} uiModeContextValues={agentUIMode("OrchestratorAgent")}>
                <TTSButton message={streamingMessage} />
            </StoryProvider>
        );
        expect(processStreamingText).not.toHaveBeenCalled();
    });

    test("does not auto-play when the gateway does not offer TTS, even with conversationMode on", () => {
        render(
            <StoryProvider configContextValues={{ textToSpeechEnabled: false }} audioSettingsContextValues={convoSettings}>
                <TTSButton message={streamingMessage} />
            </StoryProvider>
        );
        expect(processStreamingText).not.toHaveBeenCalled();
        expect(speak).not.toHaveBeenCalled();
    });
});
