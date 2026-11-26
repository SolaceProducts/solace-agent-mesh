// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

export interface SpeechSettings {
    speechToText: boolean;
    engineSTT: "browser" | "external";
    sttProvider: "browser" | "openai" | "azure";
    languageSTT: string;

    textToSpeech: boolean;
    engineTTS: "browser" | "external";
    ttsProvider: "browser" | "gemini" | "azure" | "polly";
    voice: string;
    playbackRate: number;
    automaticPlayback: boolean;
    cacheTTS: boolean;
    cloudBrowserVoices: boolean;

    conversationMode: boolean;
}

export interface AudioSettingsContextValue {
    settings: SpeechSettings;
    isInitialized: boolean;
    updateSetting: <K extends keyof SpeechSettings>(key: K, value: SpeechSettings[K]) => void;
    updateSettings: (updates: Partial<SpeechSettings>) => void;
    resetSettings: () => void;
    speechToTextEndpoint: "browser" | "external";
    textToSpeechEndpoint: "browser" | "external";
    isTTSPlaying: boolean;
    setIsTTSPlaying: (playing: boolean) => void;
    onTTSStart: () => void;
    onTTSEnd: () => void;
}

export const AudioSettingsContext = createContext<AudioSettingsContextValue | undefined>(undefined);

export function useAudioSettings(): AudioSettingsContextValue {
    const context = useContext(AudioSettingsContext);
    if (!context) {
        throw new Error("useAudioSettings must be used within AudioSettingsProvider");
    }
    return context;
}
