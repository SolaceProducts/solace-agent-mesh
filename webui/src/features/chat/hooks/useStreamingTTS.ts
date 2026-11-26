// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { api } from "@/api/client";

import { useAudioSettings } from "@/providers/contexts";

import { readAudioStream, sniffAudioMime } from "./ttsStream";

interface UseStreamingTTSOptions {
    messageId: string;
    onStart?: () => void;
    onEnd?: () => void;
    onError?: (error: string) => void;
}

interface UseStreamingTTSReturn {
    isPlaying: boolean;
    isLoading: boolean;
    error: string | null;
    processStreamingText: (text: string, isComplete: boolean) => Promise<void>;
    stop: () => void;
}

interface AudioQueueItem {
    audio: HTMLAudioElement;
    sentence: string;
    index: number;
}

export function useStreamingTTS(options: UseStreamingTTSOptions): UseStreamingTTSReturn {
    const { messageId, onStart, onEnd, onError } = options;
    const { settings, onTTSStart, onTTSEnd } = useAudioSettings();

    const [isPlaying, setIsPlaying] = useState(false);
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const processedSentencesRef = useRef<Set<string>>(new Set());
    const audioQueueRef = useRef<AudioQueueItem[]>([]);
    const isPlayingQueueRef = useRef(false);
    const currentAudioRef = useRef<HTMLAudioElement | null>(null);
    const lastTextRef = useRef<string>("");
    const lastTextLengthRef = useRef<number>(0);
    const isStoppedRef = useRef(false);
    const hasNotifiedStartRef = useRef(false);
    const isGeneratingRef = useRef(false);
    const processingPromiseRef = useRef<Promise<void> | null>(null);
    const pendingTextRef = useRef<{ text: string; isComplete: boolean } | null>(null);

    const onStartRef = useRef(onStart);
    const onEndRef = useRef(onEnd);
    const onErrorRef = useRef(onError);
    const onTTSStartRef = useRef(onTTSStart);
    const onTTSEndRef = useRef(onTTSEnd);

    useEffect(() => {
        onStartRef.current = onStart;
        onEndRef.current = onEnd;
        onErrorRef.current = onError;
        onTTSStartRef.current = onTTSStart;
        onTTSEndRef.current = onTTSEnd;
    }, [onStart, onEnd, onError, onTTSStart, onTTSEnd]);

    const extractSentences = useCallback((text: string): string[] => {
        const sentenceRegex = /[^.!?]+[.!?]+(?:\s|$)/g;
        const sentences = text.match(sentenceRegex) || [];
        return sentences.map(s => s.trim()).filter(s => s.length > 0);
    }, []);

    const generateStreamedAudio = useCallback(
        async (text: string): Promise<HTMLAudioElement | null> => {
            try {
                console.log(`[StreamingTTS] Requesting streaming TTS for text length: ${text.length}`);

                let processedText = text;
                try {
                    const preprocessed = await api.webui.post("/api/v1/speech/preprocess", {
                        text,
                        readCodeBlocks: false,
                        readImages: true,
                        readCitations: true,
                    });
                    processedText = preprocessed.text || text;
                } catch (preprocessError) {
                    console.warn("[StreamingTTS] Preprocess failed, using raw text:", preprocessError);
                }

                const response = await api.webui.post(
                    "/api/v1/speech/tts/stream",
                    {
                        input: processedText,
                        voice: settings.voice,
                        provider: settings.ttsProvider,
                    },
                    { fullResponse: true }
                );

                if (!response.ok) {
                    throw new Error(`TTS streaming failed: ${response.statusText}`);
                }

                const combinedArray = await readAudioStream(response);
                const audioBlob = new Blob([combinedArray], { type: sniffAudioMime(combinedArray) });

                const blobUrl = URL.createObjectURL(audioBlob);
                const audio = new Audio();

                audio.onerror = e => {
                    const error = audio.error;
                    console.error(`[StreamingTTS] Audio error: code=${error?.code}, message=${error?.message}`, e);
                };

                audio.src = blobUrl;
                audio.playbackRate = settings.playbackRate || 1.0;
                audio.load();

                return audio;
            } catch (err) {
                const message = err instanceof Error ? err.message : String(err);
                console.error("[StreamingTTS] Error generating streamed audio:", err);
                onErrorRef.current?.(message);
                return null;
            }
        },
        [settings.voice, settings.playbackRate, settings.ttsProvider]
    );

    const playNextInQueue = useCallback(() => {
        if (isStoppedRef.current) {
            console.log("[StreamingTTS] Skipping playback - stopped");
            return;
        }

        if (isPlayingQueueRef.current || audioQueueRef.current.length === 0) {
            return;
        }

        isPlayingQueueRef.current = true;
        const item = audioQueueRef.current.shift();

        if (!item) {
            isPlayingQueueRef.current = false;
            return;
        }

        const { audio, sentence } = item;
        currentAudioRef.current = audio;

        console.log(`[StreamingTTS] Playing sentence: "${sentence.substring(0, 50)}..."`);

        audio.onplay = () => {
            console.log("[StreamingTTS] Audio onplay event fired");
            setIsPlaying(true);
            setIsLoading(false);
            if (!hasNotifiedStartRef.current) {
                console.log("[StreamingTTS] First audio playing, notifying global state");
                hasNotifiedStartRef.current = true;
                onStartRef.current?.();
                if (onTTSStartRef.current) {
                    onTTSStartRef.current();
                }
            }
        };

        audio.onended = () => {
            console.log("[StreamingTTS] Audio onended event fired");
            URL.revokeObjectURL(audio.src);
            currentAudioRef.current = null;
            isPlayingQueueRef.current = false;

            if (audioQueueRef.current.length > 0) {
                console.log("[StreamingTTS] More audio in queue, playing next");
                playNextInQueue();
            } else if (!isGeneratingRef.current) {
                console.log("[StreamingTTS] All audio played and generation complete, notifying global state");
                setIsPlaying(false);
                onEndRef.current?.();
                if (onTTSEndRef.current) {
                    onTTSEndRef.current();
                }
            } else {
                console.log("[StreamingTTS] Queue empty but still generating, polling...");
                let pollCount = 0;
                const maxPolls = 100;
                const checkInterval = setInterval(() => {
                    pollCount++;
                    if (audioQueueRef.current.length > 0) {
                        console.log("[StreamingTTS] New audio arrived during poll, playing");
                        clearInterval(checkInterval);
                        playNextInQueue();
                    } else if (!isGeneratingRef.current && !isPlayingQueueRef.current) {
                        console.log("[StreamingTTS] Generation finished and not playing, notifying global state");
                        clearInterval(checkInterval);
                        setIsPlaying(false);
                        onEndRef.current?.();
                        if (onTTSEndRef.current) {
                            onTTSEndRef.current();
                        }
                    } else if (pollCount >= maxPolls) {
                        console.log("[StreamingTTS] Poll timeout after 10s, forcing end");
                        clearInterval(checkInterval);
                        isGeneratingRef.current = false;
                        setIsPlaying(false);
                        onEndRef.current?.();
                        if (onTTSEndRef.current) {
                            onTTSEndRef.current();
                        }
                    }
                }, 100);
            }
        };

        audio.onerror = () => {
            const errorCode = audio.error?.code;
            if (errorCode === 1) {
                console.log("[StreamingTTS] Ignoring ABORTED error (expected during stop)");
                return;
            }

            const errorMsg = `Failed to play audio (code: ${errorCode})`;
            setError(errorMsg);
            onErrorRef.current?.(errorMsg);
            URL.revokeObjectURL(audio.src);
            currentAudioRef.current = null;
            isPlayingQueueRef.current = false;

            if (audioQueueRef.current.length > 0) {
                playNextInQueue();
            } else {
                setIsPlaying(false);
                onEnd?.();
                onTTSEnd();
            }
        };

        audio.play().catch(err => {
            if (err.name === "AbortError") {
                console.log("[StreamingTTS] Ignoring AbortError from play() (expected during stop)");
                return;
            }
            console.error("[StreamingTTS] Error playing audio:", err);
            isPlayingQueueRef.current = false;
            playNextInQueue();
        });
    }, [onEnd, onTTSEnd]);

    const doProcessing = useCallback(
        async (text: string, isComplete: boolean) => {
            console.log(`[StreamingTTS] doProcessing called: textLen=${text.length}, isComplete=${isComplete}`);

            if (isStoppedRef.current) {
                console.log("[StreamingTTS] Skipping - stopped");
                return;
            }

            const sentences = extractSentences(text);
            const newText = sentences.join(" ");

            if (isComplete) {
                const lastSentence = sentences[sentences.length - 1] || "";
                const lastSentenceEnd = text.lastIndexOf(lastSentence) + lastSentence.length;
                const remainingText = text.substring(lastSentenceEnd).trim();
                if (remainingText) {
                    console.log(`[StreamingTTS] Adding remaining text: "${remainingText.substring(0, 50)}..."`);
                }
            }

            const textToProcess = isComplete ? text : newText;
            if (processedSentencesRef.current.has(textToProcess) || textToProcess.length < 10) {
                console.log("[StreamingTTS] Skipping - already processed or too short");
                return;
            }

            processedSentencesRef.current.add(textToProcess);
            lastTextRef.current = text;
            lastTextLengthRef.current = text.length;

            console.log(`[StreamingTTS] Processing text (${textToProcess.length} chars)`);

            setIsLoading(true);
            isStoppedRef.current = false;
            isGeneratingRef.current = true;

            try {
                const audio = await generateStreamedAudio(textToProcess);

                if (audio && !isStoppedRef.current) {
                    audioQueueRef.current.push({
                        audio,
                        sentence: textToProcess.substring(0, 50),
                        index: audioQueueRef.current.length,
                    });

                    console.log(`[StreamingTTS] Added audio to queue (size: ${audioQueueRef.current.length})`);

                    if (!isPlayingQueueRef.current) {
                        console.log("[StreamingTTS] Starting playback");
                        playNextInQueue();
                    }
                }
            } catch (err) {
                console.error("[StreamingTTS] Error processing:", err);
            }

            setIsLoading(false);

            if (isComplete) {
                isGeneratingRef.current = false;
                console.log(`[StreamingTTS] Message complete`);
            }
        },
        [extractSentences, generateStreamedAudio, playNextInQueue]
    );

    const processStreamingText = useCallback(
        async (text: string, isComplete: boolean) => {
            if (processingPromiseRef.current) {
                console.log("[StreamingTTS] Already processing, queuing update");
                pendingTextRef.current = { text, isComplete };
                return;
            }

            processingPromiseRef.current = doProcessing(text, isComplete);

            try {
                await processingPromiseRef.current;
            } finally {
                processingPromiseRef.current = null;

                if (pendingTextRef.current) {
                    const pending = pendingTextRef.current;
                    pendingTextRef.current = null;
                    console.log("[StreamingTTS] Processing queued update");
                    await processStreamingText(pending.text, pending.isComplete);
                }
            }
        },
        [doProcessing]
    );

    const stop = useCallback(() => {
        if (!isPlayingQueueRef.current && audioQueueRef.current.length === 0) {
            return;
        }

        console.log("[StreamingTTS] Stopping playback");
        isStoppedRef.current = true;

        if (currentAudioRef.current) {
            try {
                currentAudioRef.current.pause();
                currentAudioRef.current.currentTime = 0;
                URL.revokeObjectURL(currentAudioRef.current.src);
            } catch {}
            currentAudioRef.current = null;
        }

        audioQueueRef.current.forEach(item => {
            try {
                URL.revokeObjectURL(item.audio.src);
            } catch {}
        });
        audioQueueRef.current = [];

        isPlayingQueueRef.current = false;
        setIsPlaying(false);
        setIsLoading(false);

        if (isPlaying) {
            onEnd?.();
            onTTSEnd();
        }
    }, [isPlaying, onEnd, onTTSEnd]);

    useEffect(() => {
        const processedSentences = processedSentencesRef.current;

        processedSentences.clear();
        lastTextRef.current = "";
        lastTextLengthRef.current = 0;
        isStoppedRef.current = false;
        hasNotifiedStartRef.current = false;
        isGeneratingRef.current = false;
        processingPromiseRef.current = null;
        pendingTextRef.current = null;

        return () => {
            const processedSentences = processedSentencesRef.current;

            if (isPlayingQueueRef.current || audioQueueRef.current.length > 0) {
                console.log("[StreamingTTS] Cleanup: stopping playback for message change/unmount");
                isStoppedRef.current = true;

                if (currentAudioRef.current) {
                    try {
                        currentAudioRef.current.pause();
                        URL.revokeObjectURL(currentAudioRef.current.src);
                    } catch {}
                    currentAudioRef.current = null;
                }

                audioQueueRef.current.forEach(item => {
                    try {
                        URL.revokeObjectURL(item.audio.src);
                    } catch {}
                });
                audioQueueRef.current = [];
                isPlayingQueueRef.current = false;
            }
            processedSentences.clear();
            lastTextRef.current = "";
            lastTextLengthRef.current = 0;
            processingPromiseRef.current = null;
            pendingTextRef.current = null;
        };
    }, [messageId]);

    return {
        isPlaying,
        isLoading,
        error,
        processStreamingText,
        stop,
    };
}
