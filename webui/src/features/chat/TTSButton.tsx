// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Loader2, Volume2, VolumeX } from "lucide-react";

import { useAudioSettings, useConfigContext, useUIModeContext } from "@/providers/contexts";

import { useStreamingTTS, useTextToSpeech } from "@/features/chat/hooks";

import type { MessageFE, TextPart } from "@/types";
import { Button } from "@/ui";
import { cn } from "@/utils";

interface TTSButtonProps {
    message: MessageFE;
    className?: string;
}

const preExistingCompleteMessages = new Set<string>();

let currentlyPlayingMessageId: string | null = null;

function extractTextContent(message: MessageFE): string {
    if (!message.parts || message.parts.length === 0) {
        return "";
    }

    const textParts = message.parts.filter(p => p.kind === "text") as TextPart[];
    return textParts
        .map(p => p.text)
        .join(" ")
        .trim();
}

export const TTSButton: React.FC<TTSButtonProps> = ({ message, className }) => {
    const { settings, onTTSStart, onTTSEnd } = useAudioSettings();
    const { textToSpeechEnabled } = useConfigContext();
    const { allowSpeech } = useUIModeContext();
    const messageId = message.metadata?.messageId || "";
    const content = useMemo(() => extractTextContent(message), [message]);
    const isStreaming = !message.isComplete;

    const ttsEnabled = Boolean(textToSpeechEnabled) && allowSpeech;

    const {
        isSpeaking: isRegularSpeaking,
        isLoading: isRegularLoading,
        speak,
        stop: stopRegular,
    } = useTextToSpeech({
        onStart: () => {
            onTTSStart();
        },
        onEnd: () => {
            if (currentlyPlayingMessageId === messageId) {
                currentlyPlayingMessageId = null;
            }
            onTTSEnd();
        },
        onError: error => {
            console.error("TTS error:", error);
            if (currentlyPlayingMessageId === messageId) {
                currentlyPlayingMessageId = null;
            }
            onTTSEnd();
        },
    });

    const {
        isPlaying: isStreamingPlaying,
        isLoading: isStreamingLoading,
        processStreamingText,
        stop: stopStreaming,
    } = useStreamingTTS({
        messageId,
        onStart: () => {},
        onEnd: () => {
            if (currentlyPlayingMessageId === messageId) {
                currentlyPlayingMessageId = null;
            }
        },
        onError: error => {
            console.error("Streaming TTS error:", error);
            if (currentlyPlayingMessageId === messageId) {
                currentlyPlayingMessageId = null;
            }
        },
    });

    const isSpeaking = isStreaming ? isStreamingPlaying : isRegularSpeaking;
    const isLoading = isStreaming ? isStreamingLoading : isRegularLoading;

    const hasStartedStreaming = useRef(false);
    const hasAutoPlayed = useRef(false);
    const [wasCompleteOnMount, setWasCompleteOnMount] = useState(false);

    useEffect(() => {
        if (message.isComplete && messageId) {
            setWasCompleteOnMount(true);
            preExistingCompleteMessages.add(messageId);
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    useEffect(() => {
        const shouldAutoPlay = ttsEnabled && settings.conversationMode;

        if (shouldAutoPlay && !message.isUser && isStreaming && content && content.length > 20 && !hasStartedStreaming.current) {
            if (currentlyPlayingMessageId && currentlyPlayingMessageId !== messageId) {
                return;
            }
            currentlyPlayingMessageId = messageId;

            hasStartedStreaming.current = true;
            processStreamingText(content, false);
        } else if (shouldAutoPlay && !message.isUser && isStreaming && hasStartedStreaming.current) {
            processStreamingText(content, false);
        } else if (shouldAutoPlay && !message.isUser && message.isComplete && hasStartedStreaming.current && !hasAutoPlayed.current) {
            hasAutoPlayed.current = true;
            processStreamingText(content, true);
        } else if (shouldAutoPlay && !message.isUser && message.isComplete && !hasStartedStreaming.current && !hasAutoPlayed.current && content && !wasCompleteOnMount) {
            if (currentlyPlayingMessageId && currentlyPlayingMessageId !== messageId) {
                return;
            }
            currentlyPlayingMessageId = messageId;
            hasStartedStreaming.current = true;
            hasAutoPlayed.current = true;
            processStreamingText(content, true);
        }
    }, [ttsEnabled, settings.conversationMode, settings.automaticPlayback, message.isUser, isStreaming, message.isComplete, content, messageId, processStreamingText, wasCompleteOnMount]);

    useEffect(() => {
        const shouldAutoPlay = ttsEnabled && settings.conversationMode;

        if (shouldAutoPlay && !message.isUser && message.isComplete && content && !isSpeaking && !isLoading && !hasAutoPlayed.current && !hasStartedStreaming.current && !wasCompleteOnMount) {
            if (currentlyPlayingMessageId && currentlyPlayingMessageId !== messageId) {
                return;
            }
            currentlyPlayingMessageId = messageId;

            hasAutoPlayed.current = true;

            const timer = setTimeout(() => {
                speak(content);
            }, 100);
            return () => clearTimeout(timer);
        }
    }, [ttsEnabled, settings.conversationMode, settings.automaticPlayback, message.isUser, message.isComplete, content, isSpeaking, isLoading, messageId, speak, wasCompleteOnMount]);

    useEffect(() => {
        hasAutoPlayed.current = false;
        hasStartedStreaming.current = false;
        if (message.isComplete && messageId) {
            setWasCompleteOnMount(true);
            preExistingCompleteMessages.add(messageId);
        } else {
            setWasCompleteOnMount(false);
        }
    }, [messageId, message.isComplete]);

    const handleClick = useCallback(async () => {
        if (isSpeaking) {
            if (isStreaming) {
                stopStreaming();
            } else {
                stopRegular();
            }
        } else if (content) {
            if (isStreaming) {
                hasStartedStreaming.current = true;
                await processStreamingText(content, false);
            } else {
                await speak(content);
            }
        }
    }, [isSpeaking, isStreaming, content, speak, stopRegular, stopStreaming, processStreamingText]);

    const renderIcon = () => {
        if (isLoading) {
            return <Loader2 className="size-4 animate-spin" />;
        }

        if (isSpeaking) {
            return <VolumeX className="size-4" />;
        }

        return <Volume2 className="size-4" />;
    };

    const getTooltip = () => {
        if (isSpeaking) {
            return "Stop reading";
        }
        if (isLoading) {
            return "Generating audio...";
        }
        if (!settings.textToSpeech) {
            return "Text-to-speech is disabled";
        }
        return "Read aloud";
    };

    if (!ttsEnabled || !settings.textToSpeech || !content) {
        return null;
    }

    return (
        <Button
            variant="ghost"
            size="icon"
            onClick={handleClick}
            disabled={isLoading || !content}
            className={cn("size-8 transition-colors", isSpeaking && "bg-(--info-w10) hover:bg-(--info-w20)", className)}
            tooltip={getTooltip()}
            aria-label={isSpeaking ? "Stop reading aloud" : "Read aloud"}
            aria-pressed={isSpeaking}
            aria-busy={isLoading}
        >
            {renderIcon()}
        </Button>
    );
};
