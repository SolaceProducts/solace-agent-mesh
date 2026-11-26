// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { AudioSettingsContext, type AudioSettingsContextValue, type SpeechSettings } from "@/providers/contexts/AudioSettingsContext";

const defaultSpeechToTextSettings: SpeechSettings = {
    speechToText: true,
    engineSTT: "browser",
    sttProvider: "browser",
    languageSTT: "",

    textToSpeech: false,
    engineTTS: "browser",
    ttsProvider: "browser",
    voice: "",
    playbackRate: 1,
    automaticPlayback: false,
    cacheTTS: false,
    cloudBrowserVoices: false,

    conversationMode: false,
};

const defaultMockAudioSettingsValues: AudioSettingsContextValue = {
    settings: defaultSpeechToTextSettings,
    isInitialized: false,
    updateSetting: () => {},
    updateSettings: () => {},
    resetSettings: () => {},
    speechToTextEndpoint: "browser",
    textToSpeechEndpoint: "browser",
    isTTSPlaying: false,
    setIsTTSPlaying: () => {},
    onTTSStart: () => {},
    onTTSEnd: () => {},
};

interface MockAudioSettingsProviderProps {
    children: React.ReactNode;
    mockValues: Partial<AudioSettingsContextValue>;
}

export const MockAudioSettingsProvider: React.FC<MockAudioSettingsProviderProps> = ({ children, mockValues }) => {
    const contextValues = {
        ...defaultMockAudioSettingsValues,
        ...mockValues,
    };

    return <AudioSettingsContext.Provider value={contextValues}>{children}</AudioSettingsContext.Provider>;
};
