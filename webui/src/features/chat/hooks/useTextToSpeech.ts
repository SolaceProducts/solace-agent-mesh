// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { api } from "@/api/client";

import { useAudioSettings } from "@/providers/contexts";

import { readAudioStream, sniffAudioMime } from "./ttsStream";

interface UseTextToSpeechOptions {
    onStart?: () => void;
    onEnd?: () => void;
    onError?: (error: string) => void;
}

interface UseTextToSpeechReturn {
    isSpeaking: boolean;
    isLoading: boolean;
    error: string | null;
    voices: VoiceOption[];
    speak: (text: string) => Promise<void>;
    stop: () => void;
    pause: () => void;
    resume: () => void;
}

export interface VoiceOption {
    value: string;
    label: string;
}

const TTS_CACHE = "tts-responses";

const ttsCacheKey = (text: string, voice: string, provider: string): string => `${text}-${voice}-${provider || "default"}`;

async function readTtsCache(key: string): Promise<Blob | null> {
    if (typeof caches === "undefined") return null;
    try {
        const cache = await caches.open(TTS_CACHE);
        const hit = await cache.match(key);
        return hit ? await hit.blob() : null;
    } catch {
        return null;
    }
}

async function writeTtsCache(key: string, blob: Blob): Promise<void> {
    if (typeof caches === "undefined") return;
    try {
        const cache = await caches.open(TTS_CACHE);
        await cache.put(key, new Response(blob, { headers: { "Content-Type": blob.type, "Content-Length": blob.size.toString() } }));
    } catch {}
}

export function useTextToSpeech(options: UseTextToSpeechOptions = {}): UseTextToSpeechReturn {
    const { onStart, onEnd, onError } = options;
    const { settings } = useAudioSettings();

    const [isSpeaking, setIsSpeaking] = useState(false);
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [voices, setVoices] = useState<VoiceOption[]>([]);

    const utteranceRef = useRef<SpeechSynthesisUtterance | null>(null);

    const audioRef = useRef<HTMLAudioElement | null>(null);
    const audioUrlRef = useRef<string | null>(null);

    const isBrowserMode = settings.engineTTS === "browser";

    useEffect(() => {
        if (!isBrowserMode) return;

        const loadVoices = () => {
            const synth = window.speechSynthesis;
            if (!synth) return;

            const availableVoices = synth.getVoices();
            const voiceOptions: VoiceOption[] = availableVoices
                .filter(voice => {
                    if (settings.cloudBrowserVoices) {
                        return true;
                    }
                    return voice.localService;
                })
                .map(voice => ({
                    value: voice.name,
                    label: `${voice.name} (${voice.lang})`,
                }));

            setVoices(voiceOptions);
        };

        loadVoices();

        if (window.speechSynthesis && window.speechSynthesis.onvoiceschanged !== undefined) {
            window.speechSynthesis.onvoiceschanged = loadVoices;
        }

        return () => {
            if (window.speechSynthesis && window.speechSynthesis.onvoiceschanged) {
                window.speechSynthesis.onvoiceschanged = null;
            }
        };
    }, [isBrowserMode, settings.cloudBrowserVoices]);

    useEffect(() => {
        if (isBrowserMode) return;

        const loadExternalVoices = async () => {
            try {
                const provider = settings.ttsProvider || "gemini";
                const data = await api.webui.get(`/api/v1/speech/voices?provider=${provider}`);
                const voiceOptions: VoiceOption[] = (data.voices || []).map((voice: string) => ({
                    value: voice,
                    label: voice,
                }));
                setVoices(voiceOptions);
            } catch (err) {
                console.error("Failed to load external voices:", err);
            }
        };

        loadExternalVoices();
    }, [isBrowserMode, settings.ttsProvider]);

    const cleanup = useCallback(() => {
        if (utteranceRef.current) {
            window.speechSynthesis.cancel();
            utteranceRef.current = null;
        }

        if (audioRef.current) {
            audioRef.current.pause();
            audioRef.current.src = "";
            audioRef.current = null;
        }

        if (audioUrlRef.current) {
            URL.revokeObjectURL(audioUrlRef.current);
            audioUrlRef.current = null;
        }
    }, []);

    const speakBrowser = useCallback(
        async (text: string) => {
            const synth = window.speechSynthesis;

            if (!synth) {
                const errorMsg = "Speech synthesis is not supported in this browser";
                setError(errorMsg);
                onError?.(errorMsg);
                return;
            }

            try {
                synth.cancel();

                let processedText = text;
                try {
                    const response = await api.webui.post("/api/v1/speech/preprocess", {
                        text,
                        readCodeBlocks: false,
                        readImages: true,
                        readCitations: true,
                    });
                    processedText = response.text || text;
                } catch (preprocessError) {
                    console.warn("[TTS] Markdown preprocessing failed, using original text:", preprocessError);
                }

                const utterance = new SpeechSynthesisUtterance(processedText);
                utterance.rate = settings.playbackRate || 1.0;
                utterance.lang = settings.languageSTT || "en-US";

                if (settings.voice) {
                    const availableVoices = synth.getVoices();
                    const selectedVoice = availableVoices.find(v => v.name === settings.voice);
                    if (selectedVoice) {
                        utterance.voice = selectedVoice;
                    }
                }

                utterance.onstart = () => {
                    setIsSpeaking(true);
                    setError(null);
                    onStart?.();
                };

                utterance.onend = () => {
                    setIsSpeaking(false);
                    utteranceRef.current = null;
                    onEnd?.();
                };

                utterance.onerror = event => {
                    const errorMsg = `Speech synthesis error: ${event.error}`;
                    setError(errorMsg);
                    onError?.(errorMsg);
                    setIsSpeaking(false);
                    utteranceRef.current = null;
                };

                utteranceRef.current = utterance;
                synth.speak(utterance);
            } catch (err) {
                const errorMsg = `Failed to speak: ${err}`;
                setError(errorMsg);
                onError?.(errorMsg);
            }
        },
        [settings.playbackRate, settings.languageSTT, settings.voice, onStart, onEnd, onError]
    );

    const playAudioBlob = useCallback(
        async (audioBlob: Blob) => {
            const blobUrl = URL.createObjectURL(audioBlob);
            const audio = new Audio(blobUrl);
            audio.playbackRate = settings.playbackRate || 1.0;

            audio.onplay = () => {
                setIsSpeaking(true);
                setError(null);
                onStart?.();
            };
            audio.onended = () => {
                setIsSpeaking(false);
                URL.revokeObjectURL(blobUrl);
                audioRef.current = null;
                audioUrlRef.current = null;
                onEnd?.();
            };
            audio.onerror = () => {
                const errorMsg = "Failed to play audio";
                setError(errorMsg);
                onError?.(errorMsg);
                setIsSpeaking(false);
                URL.revokeObjectURL(blobUrl);
                audioRef.current = null;
                audioUrlRef.current = null;
            };

            audioRef.current = audio;
            audioUrlRef.current = blobUrl;

            try {
                await audio.play();
            } catch (playError: unknown) {
                URL.revokeObjectURL(blobUrl);
                audioRef.current = null;
                audioUrlRef.current = null;
                if (playError instanceof Error && playError.name === "NotAllowedError") {
                    throw new Error("Click the speaker button again to play audio (browser autoplay policy)", { cause: playError });
                }
                throw playError;
            }
        },
        [settings.playbackRate, onStart, onEnd, onError]
    );

    const speakExternal = useCallback(
        async (text: string) => {
            cleanup();
            setIsLoading(true);

            const cacheKey = ttsCacheKey(text, settings.voice, settings.ttsProvider ?? "");
            try {
                if (settings.cacheTTS) {
                    const cached = await readTtsCache(cacheKey);
                    if (cached) {
                        await playAudioBlob(cached);
                        return;
                    }
                }

                const response = await api.webui.post("/api/v1/speech/tts/stream", { input: text, voice: settings.voice, provider: settings.ttsProvider }, { fullResponse: true });
                if (!response.ok) {
                    throw new Error(`TTS request failed: ${response.statusText}`);
                }

                const audioBytes = await readAudioStream(response);
                const audioBlob = new Blob([audioBytes], { type: sniffAudioMime(audioBytes) });

                if (settings.cacheTTS) {
                    await writeTtsCache(cacheKey, audioBlob);
                }

                await playAudioBlob(audioBlob);
            } catch (err) {
                const errorMsg = err instanceof Error ? err.message : String(err);
                setError(errorMsg);
                onError?.(errorMsg);
            } finally {
                setIsLoading(false);
            }
        },
        [settings.cacheTTS, settings.voice, settings.ttsProvider, playAudioBlob, onError, cleanup]
    );

    const speak = useCallback(
        async (text: string) => {
            if (!text || !text.trim()) {
                return;
            }

            setError(null);

            if (isBrowserMode) {
                await speakBrowser(text);
            } else {
                await speakExternal(text);
            }
        },
        [isBrowserMode, speakBrowser, speakExternal]
    );

    const stop = useCallback(() => {
        if (isBrowserMode) {
            window.speechSynthesis.cancel();
            setIsSpeaking(false);
        } else {
            if (audioRef.current) {
                audioRef.current.pause();
                audioRef.current.currentTime = 0;
            }
            setIsSpeaking(false);
        }
    }, [isBrowserMode]);

    const pause = useCallback(() => {
        if (isBrowserMode) {
            window.speechSynthesis.pause();
        } else if (audioRef.current) {
            audioRef.current.pause();
        }
        setIsSpeaking(false);
    }, [isBrowserMode]);

    const resume = useCallback(() => {
        if (isBrowserMode) {
            window.speechSynthesis.resume();
            setIsSpeaking(true);
        } else if (audioRef.current) {
            audioRef.current.play();
            setIsSpeaking(true);
        }
    }, [isBrowserMode]);

    useEffect(() => {
        return () => {
            cleanup();
        };
    }, [cleanup]);

    return {
        isSpeaking,
        isLoading,
        error,
        voices,
        speak,
        stop,
        pause,
        resume,
    };
}
