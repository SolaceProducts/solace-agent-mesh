// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef } from "react";
import { Loader2, Mic } from "lucide-react";

import { useAudioSettings } from "@/providers/contexts";

import { Button } from "@/ui";
import { cn } from "@/utils";

import { useSpeechToText } from "./useSpeechToText";

interface AudioRecorderProps {
    disabled?: boolean;
    onTranscriptionComplete: (text: string) => void;
    onError?: (error: string) => void;
    onRecordingStateChange?: (isRecording: boolean) => void;
    className?: string;
}

export interface AudioRecorderRef {
    startRecording: () => Promise<void>;
    stopRecording: () => Promise<void>;
    cancelRecording: () => Promise<void>;
}

export const AudioRecorder = forwardRef<AudioRecorderRef, AudioRecorderProps>(({ disabled = false, onTranscriptionComplete, onError: onErrorProp, onRecordingStateChange, className }, ref) => {
    const { settings } = useAudioSettings();
    const existingTextRef = useRef<string>("");
    const shouldSendTranscriptionRef = useRef<boolean>(true);

    const handleTranscriptionUpdate = useCallback((text: string) => {
        console.log("Transcription update:", text);
    }, []);

    const handleTranscriptionComplete = useCallback(
        (text: string) => {
            if (shouldSendTranscriptionRef.current && text && text.trim()) {
                const finalText = existingTextRef.current ? `${existingTextRef.current} ${text}`.trim() : text.trim();

                onTranscriptionComplete(finalText);
                existingTextRef.current = "";
            } else if (!shouldSendTranscriptionRef.current) {
                console.log("AudioRecorder: Transcription canceled, not sending");
                existingTextRef.current = "";
            }
            shouldSendTranscriptionRef.current = true;
        },
        [onTranscriptionComplete]
    );

    const handleError = useCallback(
        (error: string) => {
            console.error("Speech-to-text error:", error);
            if (onErrorProp) {
                onErrorProp(error);
            }
        },
        [onErrorProp]
    );

    const { isListening, isLoading, startRecording, stopRecording } = useSpeechToText({
        onTranscriptionComplete: handleTranscriptionComplete,
        onTranscriptionUpdate: handleTranscriptionUpdate,
        onError: handleError,
    });

    useEffect(() => {
        onRecordingStateChange?.(isListening);
    }, [isListening, onRecordingStateChange]);

    useImperativeHandle(
        ref,
        () => ({
            startRecording: async () => {
                if (!isListening) {
                    existingTextRef.current = "";
                    shouldSendTranscriptionRef.current = true;
                    await startRecording();
                }
            },
            stopRecording: async () => {
                if (isListening) {
                    shouldSendTranscriptionRef.current = true;
                    await stopRecording();
                }
            },
            cancelRecording: async () => {
                if (isListening) {
                    console.log("AudioRecorder: Canceling recording");
                    shouldSendTranscriptionRef.current = false;
                    await stopRecording();
                }
            },
        }),
        [isListening, startRecording, stopRecording]
    );

    const handleClick = useCallback(async () => {
        if (isListening) {
            shouldSendTranscriptionRef.current = true;
            await stopRecording();
        } else {
            existingTextRef.current = "";
            shouldSendTranscriptionRef.current = true;
            await startRecording();
        }
    }, [isListening, startRecording, stopRecording]);

    const renderIcon = () => {
        if (isLoading) {
            return <Loader2 className="size-5 animate-spin" />;
        }

        if (isListening) {
            return <Mic className="size-5 animate-pulse" />;
        }

        return <Mic className="size-5" />;
    };

    const getTooltip = () => {
        if (isListening) {
            return "Stop recording";
        }
        if (isLoading) {
            return "Processing...";
        }
        if (!settings.speechToText) {
            return "Speech-to-text is disabled";
        }
        return "Start voice recording";
    };

    const getAriaLabel = () => {
        if (isListening) {
            return "Stop voice recording";
        }
        return "Start voice recording";
    };

    if (!settings.speechToText) {
        return null;
    }

    return (
        <Button
            variant="ghost"
            size="icon"
            onClick={handleClick}
            disabled={disabled || isLoading}
            className={cn("transition-colors", isListening && "bg-(--background-w20) text-(--primary-wMain) hover:bg-(--background-w20)/80", className)}
            tooltip={getTooltip()}
            aria-label={getAriaLabel()}
            aria-pressed={isListening}
            aria-busy={isLoading}
        >
            {renderIcon()}
        </Button>
    );
});

AudioRecorder.displayName = "AudioRecorder";
