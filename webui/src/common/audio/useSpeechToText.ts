// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { api } from "@/api/client";

import { useAudioSettings } from "@/providers/contexts";

import { getErrorMessage } from "@/utils/api";

import { buildSttFormData } from "./wav";

interface UseSpeechToTextOptions {
    onTranscriptionComplete?: (text: string) => void;
    onTranscriptionUpdate?: (text: string) => void;
    onError?: (error: string) => void;
}

interface UseSpeechToTextReturn {
    isListening: boolean;
    isLoading: boolean;
    error: string | null;
    transcript: string;
    startRecording: () => Promise<void>;
    stopRecording: () => Promise<void>;
    resetTranscript: () => void;
}

interface SpeechRecognitionEvent extends Event {
    results: SpeechRecognitionResultList;
    resultIndex: number;
}

interface SpeechRecognitionResultList {
    length: number;
    item(index: number): SpeechRecognitionResult;
    [index: number]: SpeechRecognitionResult;
}

interface SpeechRecognitionResult {
    isFinal: boolean;
    length: number;
    item(index: number): SpeechRecognitionAlternative;
    [index: number]: SpeechRecognitionAlternative;
}

interface SpeechRecognitionAlternative {
    transcript: string;
    confidence: number;
}

interface SpeechRecognitionErrorEvent extends Event {
    error: string;
    message: string;
}

interface SpeechRecognition extends EventTarget {
    continuous: boolean;
    interimResults: boolean;
    lang: string;
    start(): void;
    stop(): void;
    abort(): void;
    onstart: ((this: SpeechRecognition, ev: Event) => void) | null;
    onend: ((this: SpeechRecognition, ev: Event) => void) | null;
    onerror: ((this: SpeechRecognition, ev: SpeechRecognitionErrorEvent) => void) | null;
    onresult: ((this: SpeechRecognition, ev: SpeechRecognitionEvent) => void) | null;
}

declare global {
    interface Window {
        SpeechRecognition: new () => SpeechRecognition;
        webkitSpeechRecognition: new () => SpeechRecognition;
    }
}

function getBestSupportedMimeType(): string {
    const types = ["audio/webm", "audio/webm;codecs=opus", "audio/mp4", "audio/ogg;codecs=opus", "audio/ogg", "audio/wav"];

    for (const type of types) {
        if (MediaRecorder.isTypeSupported(type)) {
            return type;
        }
    }

    const ua = navigator.userAgent.toLowerCase();
    if (ua.indexOf("safari") !== -1 && ua.indexOf("chrome") === -1) {
        return "audio/mp4";
    } else if (ua.indexOf("firefox") !== -1) {
        return "audio/ogg";
    }

    return "audio/webm";
}

function startSingleContainerRecording(recorder: MediaRecorder): void {
    recorder.start();
}

export function useSpeechToText(options: UseSpeechToTextOptions = {}): UseSpeechToTextReturn {
    const { onTranscriptionComplete, onTranscriptionUpdate, onError } = options;
    const { settings, updateSetting } = useAudioSettings();

    const [isListening, setIsListening] = useState(false);
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [transcript, setTranscript] = useState("");

    const recognitionRef = useRef<SpeechRecognition | null>(null);

    const mediaRecorderRef = useRef<MediaRecorder | null>(null);
    const audioChunksRef = useRef<Blob[]>([]);
    const audioStreamRef = useRef<MediaStream | null>(null);

    const isBrowserMode = settings.engineSTT === "browser";

    const cleanup = useCallback(() => {
        if (recognitionRef.current) {
            try {
                recognitionRef.current.stop();
            } catch {}
            recognitionRef.current = null;
        }

        if (mediaRecorderRef.current && mediaRecorderRef.current.state !== "inactive") {
            try {
                mediaRecorderRef.current.stop();
            } catch {}
        }

        if (audioStreamRef.current) {
            audioStreamRef.current.getTracks().forEach(track => track.stop());
            audioStreamRef.current = null;
        }

        audioChunksRef.current = [];
    }, []);

    const startBrowserRecording = useCallback(async () => {
        const SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;

        if (!SpeechRecognition) {
            const errorMsg = "Speech recognition is not supported in this browser. Please use Chrome, Edge, or Safari, or switch to External API mode in settings.";
            setError(errorMsg);
            onError?.(errorMsg);
            console.error("Browser STT not supported. Available:", {
                SpeechRecognition: !!window.SpeechRecognition,
                webkitSpeechRecognition: !!window.webkitSpeechRecognition,
                userAgent: navigator.userAgent,
            });
            return;
        }

        try {
            const recognition = new SpeechRecognition();
            recognition.continuous = true;
            recognition.interimResults = true;
            recognition.lang = settings.languageSTT || "en-US";

            recognition.onstart = () => {
                setIsListening(true);
                setError(null);
            };

            recognition.onresult = (event: SpeechRecognitionEvent) => {
                let interimTranscript = "";
                let finalTranscript = "";

                for (let i = event.resultIndex; i < event.results.length; i++) {
                    const result = event.results[i];
                    const transcriptPart = result[0].transcript;

                    if (result.isFinal) {
                        finalTranscript += transcriptPart + " ";
                    } else {
                        interimTranscript += transcriptPart;
                    }
                }

                const fullTranscript = (finalTranscript + interimTranscript).trim();
                setTranscript(fullTranscript);
                onTranscriptionUpdate?.(fullTranscript);

                if (finalTranscript) {
                    onTranscriptionComplete?.(finalTranscript.trim());
                }
            };

            recognition.onerror = (event: SpeechRecognitionErrorEvent) => {
                const errorMsg = `Speech recognition error: ${event.error}`;
                setError(errorMsg);
                onError?.(errorMsg);
                setIsListening(false);
            };

            recognition.onend = () => {
                setIsListening(false);
            };

            recognitionRef.current = recognition;
            recognition.start();
        } catch (err) {
            const errorMsg = `Failed to start speech recognition: ${err}`;
            setError(errorMsg);
            onError?.(errorMsg);
        }
    }, [settings.languageSTT, onTranscriptionComplete, onTranscriptionUpdate, onError]);

    const stopBrowserRecording = useCallback(async () => {
        if (recognitionRef.current) {
            recognitionRef.current.stop();
            recognitionRef.current = null;
        }
        setIsListening(false);
    }, []);

    const startExternalRecording = useCallback(async () => {
        try {
            const config = await api.webui.get("/api/v1/speech/config");

            if (!config.sttExternal) {
                updateSetting("engineSTT", "browser");

                const errorMsg = "External STT is not configured. Switched to Browser mode. Please click the microphone button again.";
                setError(errorMsg);
                onError?.(errorMsg);

                return;
            }
        } catch (error) {
            const errorMsg = `Failed to check STT configuration: ${getErrorMessage(error, "Please try again.")}`;
            setError(errorMsg);
            onError?.(errorMsg);
            return;
        }

        try {
            const stream = await navigator.mediaDevices.getUserMedia({
                audio: true,
                video: false,
            });

            audioStreamRef.current = stream;
            audioChunksRef.current = [];

            const mimeType = getBestSupportedMimeType();
            const mediaRecorder = new MediaRecorder(stream, { mimeType });

            mediaRecorder.ondataavailable = (event: BlobEvent) => {
                if (event.data.size > 0) {
                    audioChunksRef.current.push(event.data);
                }
            };

            mediaRecorder.onstop = async () => {
                setIsLoading(true);

                try {
                    if (audioChunksRef.current.length === 0) {
                        throw new Error("No audio data recorded");
                    }

                    const recordedBlob = new Blob(audioChunksRef.current, { type: mimeType });

                    const formData = await buildSttFormData(recordedBlob, mimeType, {
                        provider: settings.sttProvider,
                        language: settings.languageSTT,
                    });

                    const response = await api.webui.post("/api/v1/speech/stt", formData, { fullResponse: true });

                    if (!response.ok) {
                        const errorText = await response.text();
                        let backendMessage = "";
                        try {
                            const errorJson = JSON.parse(errorText);
                            backendMessage = errorJson.message || "";
                        } catch {
                            backendMessage = errorText;
                        }
                        throw new Error(backendMessage || `Transcription failed: ${response.statusText}`);
                    }

                    const result = await response.json();
                    const transcribedText = result.text || "";

                    setTranscript(transcribedText);
                    onTranscriptionComplete?.(transcribedText);
                } catch (err) {
                    console.error("STT transcription failed", err);
                    const errorMsg = `Transcription error: ${getErrorMessage(err, "Please try again.")}`;
                    setError(errorMsg);
                    onError?.(errorMsg);
                } finally {
                    setIsLoading(false);
                    cleanup();
                }
            };

            mediaRecorderRef.current = mediaRecorder;
            startSingleContainerRecording(mediaRecorder);
            setIsListening(true);
            setError(null);
        } catch (err) {
            const errorMsg = `Failed to access microphone: ${err}`;
            setError(errorMsg);
            onError?.(errorMsg);
            cleanup();
        }
    }, [settings.sttProvider, settings.languageSTT, onTranscriptionComplete, onError, cleanup, updateSetting]);

    const stopExternalRecording = useCallback(async () => {
        if (mediaRecorderRef.current && mediaRecorderRef.current.state !== "inactive") {
            mediaRecorderRef.current.stop();
        }
        setIsListening(false);
    }, []);

    const startRecording = useCallback(async () => {
        setError(null);
        setTranscript("");

        if (isBrowserMode) {
            await startBrowserRecording();
        } else {
            await startExternalRecording();
        }
    }, [isBrowserMode, startBrowserRecording, startExternalRecording]);

    const stopRecording = useCallback(async () => {
        if (isBrowserMode) {
            await stopBrowserRecording();
        } else {
            await stopExternalRecording();
        }
    }, [isBrowserMode, stopBrowserRecording, stopExternalRecording]);

    const resetTranscript = useCallback(() => {
        setTranscript("");
        setError(null);
    }, []);

    useEffect(() => {
        return () => {
            cleanup();
        };
    }, [cleanup]);

    return {
        isListening,
        isLoading,
        error,
        transcript,
        startRecording,
        stopRecording,
        resetTranscript,
    };
}
