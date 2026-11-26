// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useRef, useState } from "react";
import { Loader2, Send, Sparkles } from "lucide-react";

import { api } from "@/api/client";

import { useAudioSettings, useConfigContext } from "@/providers/contexts";

import { AudioRecorder } from "@/common/audio";
import { InlineNotice } from "@/common/banners/InlineNotice";
import { MessageBanner } from "@/common/banners/MessageBanner";
import { MarkdownWrapper } from "@/common/markdown";
import { BreathingDot } from "@/common/status";

import { Button } from "@/ui/button";
import { Textarea } from "@/ui/textarea";

import type { TemplateConfig } from "@/types";
import { cn, isImeComposing } from "@/utils";

interface Message {
    role: "user" | "assistant";
    content: string;
    timestamp: Date;
    isError?: boolean;
    isWarning?: boolean;
}

interface ChatResponse {
    message: string;
    templateUpdates: Record<string, unknown>;
    confidence: number;
    readyToSave: boolean;
    isError?: boolean;
    isWarning?: boolean;
}

interface PromptBuilderChatProps {
    onConfigUpdate: (config: Partial<TemplateConfig>) => void;
    currentConfig: TemplateConfig;
    onReadyToSave: (ready: boolean) => void;
    initialMessage?: string | null;
    prefillOnly?: boolean;
    isEditing?: boolean;
}

export const PromptBuilderChat: React.FC<PromptBuilderChatProps> = ({ onConfigUpdate, currentConfig, onReadyToSave, initialMessage, prefillOnly = false, isEditing = false }) => {
    const [messages, setMessages] = useState<Message[]>([]);
    const [input, setInput] = useState("");
    const [isLoading, setIsLoading] = useState(false);
    const [isInitializing, setIsInitializing] = useState(true);
    const [hasUserMessage, setHasUserMessage] = useState(false);
    const messagesEndRef = useRef<HTMLDivElement>(null);
    const inputRef = useRef<HTMLTextAreaElement>(null);
    const initRef = useRef(false);

    const { settings } = useAudioSettings();
    const { speechToTextEnabled } = useConfigContext();
    const sttEnabled = Boolean(speechToTextEnabled);
    const [sttError, setSttError] = useState<string | null>(null);
    const [isRecording, setIsRecording] = useState(false);

    const scrollToBottom = () => {
        messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
    };

    useEffect(() => {
        scrollToBottom();
    }, [messages]);

    useEffect(() => {
        if (initRef.current) return;
        initRef.current = true;

        const initChat = async () => {
            try {
                const data = await api.webui.get("/api/v1/prompts/chat/init");

                const greetingMessage = isEditing ? "Hi! I'll help you edit this prompt template. What changes would you like to make?" : data.message;

                setMessages([
                    {
                        role: "assistant",
                        content: greetingMessage,
                        timestamp: new Date(),
                    },
                ]);

                if (initialMessage) {
                    if (prefillOnly) {
                        setInput(initialMessage);
                        setIsInitializing(false);
                        return;
                    }

                    setHasUserMessage(true);
                    const userMessage: Message = {
                        role: "user",
                        content: initialMessage,
                        timestamp: new Date(),
                    };
                    setMessages(prev => [...prev, userMessage]);
                    setTimeout(() => scrollToBottom(), 100);
                    setIsLoading(true);

                    try {
                        const chatResponse = await api.webui.post(
                            "/api/v1/prompts/chat",
                            {
                                message: initialMessage,
                                conversationHistory: [
                                    {
                                        role: "assistant",
                                        content: data.message,
                                    },
                                ],
                                currentTemplate: currentConfig,
                            },
                            { fullResponse: true }
                        );

                        if (chatResponse.ok) {
                            const chatData: ChatResponse = await chatResponse.json();

                            const assistantMessage: Message = {
                                role: "assistant",
                                content: chatData.message,
                                timestamp: new Date(),
                                isError: chatData.isError,
                                isWarning: chatData.isWarning,
                            };
                            setMessages(prev => [...prev, assistantMessage]);

                            if (!chatData.isError && Object.keys(chatData.templateUpdates).length > 0) {
                                onConfigUpdate(chatData.templateUpdates);
                            }

                            if (!chatData.isError) onReadyToSave(chatData.readyToSave);

                            setTimeout(() => scrollToBottom(), 100);
                        } else {
                            const errorData = await chatResponse.json().catch(() => ({}));
                            console.error("Prompt builder API error:", errorData);

                            const errorMessage: Message = {
                                role: "assistant",
                                content: "The conversation history is too long for automatic processing. Please describe your task manually, and I'll help you create a template.",
                                timestamp: new Date(),
                                isError: true,
                            };
                            setMessages(prev => [...prev, errorMessage]);
                        }
                    } catch (error) {
                        console.error("Error sending initial message:", error);
                        const errorMessage: Message = {
                            role: "assistant",
                            content: "I encountered an error processing your request. Please try describing your task manually.",
                            timestamp: new Date(),
                            isError: true,
                        };
                        setMessages(prev => [...prev, errorMessage]);
                    } finally {
                        setIsLoading(false);
                    }
                }
            } catch (error) {
                console.error("Failed to initialize chat:", error);
                const fallbackMessage = isEditing ? "Hi! I'll help you edit this prompt template. What changes would you like to make?" : "Hi! I'll help you create a prompt template. What kind of recurring task would you like to template?";

                setMessages([
                    {
                        role: "assistant",
                        content: fallbackMessage,
                        timestamp: new Date(),
                    },
                ]);
            } finally {
                setIsInitializing(false);
            }
        };

        initChat();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    useEffect(() => {
        if (!isInitializing && !isLoading && inputRef.current) {
            inputRef.current.focus();
        }
    }, [isInitializing, isLoading]);

    useEffect(() => {
        const textarea = inputRef.current;
        if (!textarea) return;

        const adjustHeight = () => {
            textarea.style.height = "auto";
            const newHeight = Math.min(textarea.scrollHeight, 200);
            textarea.style.height = `${newHeight}px`;
        };

        adjustHeight();
    }, [input]);

    const handleTranscription = useCallback(
        (text: string) => {
            const newText = input ? `${input} ${text}` : text;
            setInput(newText);

            setTimeout(() => {
                inputRef.current?.focus();
            }, 100);
        },
        [input]
    );

    const handleTranscriptionError = useCallback((error: string) => {
        setSttError(error);
    }, []);

    const handleSend = async () => {
        if (!input.trim() || isLoading) return;

        const userMessage: Message = {
            role: "user",
            content: input.trim(),
            timestamp: new Date(),
        };

        setMessages(prev => [...prev, userMessage]);
        setInput("");
        setIsLoading(true);
        setHasUserMessage(true);

        try {
            const data: ChatResponse = await api.webui.post("/api/v1/prompts/chat", {
                message: userMessage.content,
                conversationHistory: messages
                    .filter(m => !m.isError && m.content && m.content.trim().length > 0)
                    .map(m => ({
                        role: m.role,
                        content: m.content,
                    })),
                currentTemplate: currentConfig,
            });

            const assistantMessage: Message = {
                role: "assistant",
                content: data.message,
                timestamp: new Date(),
                isError: data.isError,
                isWarning: data.isWarning,
            };
            setMessages(prev => [...prev, assistantMessage]);

            if (!data.isError && Object.keys(data.templateUpdates).length > 0) {
                onConfigUpdate(data.templateUpdates);
            }

            if (!data.isError) onReadyToSave(data.readyToSave);
        } catch (error) {
            console.error("Error sending message:", error);
            const errorMessage: Message = {
                role: "assistant",
                content: "I encountered an error. Could you please try again?",
                timestamp: new Date(),
                isError: true,
            };
            setMessages(prev => [...prev, errorMessage]);
        } finally {
            setIsLoading(false);
            setTimeout(() => inputRef.current?.focus(), 100);
        }
    };

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        handleSend();
    };

    if (isInitializing) {
        return (
            <div className="flex h-full items-center justify-center">
                <div className="flex flex-col items-center gap-3">
                    <Loader2 className="h-8 w-8 animate-spin text-(--primary-wMain)" />
                    <p className="text-sm text-(--secondary-text-wMain)">Initializing AI assistant...</p>
                </div>
            </div>
        );
    }

    return (
        <div className="flex h-full flex-col">
            <div className="hidden border-b px-4 py-3 sm:block">
                <div className="flex items-center gap-2">
                    <div className="flex h-8 w-8 items-center justify-center rounded-full bg-(--primary-w10)">
                        <Sparkles className="h-4 w-4 text-(--primary-wMain)" />
                    </div>
                    <h3 className="text-sm font-semibold">AI Builder</h3>
                </div>
            </div>

            <div className="flex-1 space-y-4 overflow-y-auto p-4">
                {messages.map((message, index) => (
                    <div key={index} className={`flex ${message.role === "user" ? "justify-end" : "justify-start"}`}>
                        {message.isError ? (
                            <div className="max-w-[80%]">
                                <InlineNotice boxed variant={message.isWarning ? "warning" : "error"} className="text-sm whitespace-pre-wrap">
                                    {message.content}
                                </InlineNotice>
                            </div>
                        ) : (
                            <div className={cn("max-w-[80%] rounded-2xl px-4 py-3", message.role === "user" && "bg-(--secondary-w20)")}>
                                {message.role === "assistant" ? <MarkdownWrapper content={message.content} className="text-sm leading-relaxed" /> : <div className="text-sm leading-relaxed whitespace-pre-wrap">{message.content}</div>}
                            </div>
                        )}
                    </div>
                ))}

                {isLoading && (
                    <div className="flex justify-start">
                        <div className="flex items-center px-2 py-1">
                            <BreathingDot />
                        </div>
                    </div>
                )}

                <div ref={messagesEndRef} />
            </div>

            <div className="border-t bg-(--background-w10) p-4">
                {sttError && (
                    <div className="mb-3">
                        <MessageBanner variant="error" message={sttError} dismissible onDismiss={() => setSttError(null)} />
                    </div>
                )}

                <form onSubmit={handleSubmit} className="relative">
                    <Textarea
                        ref={inputRef}
                        value={input}
                        onChange={e => setInput(e.target.value)}
                        onKeyDown={e => {
                            if (e.key === "Enter" && !e.shiftKey && !isImeComposing(e)) {
                                e.preventDefault();
                                handleSend();
                            }
                        }}
                        placeholder={isRecording ? "Recording..." : hasUserMessage ? "Type your message..." : "Describe your recurring task..."}
                        disabled={isLoading || isRecording}
                        className="max-h-[200px] min-h-[40px] resize-none overflow-y-auto pr-24"
                        rows={1}
                        style={{ height: "40px" }}
                    />
                    <div className="absolute top-1/2 right-2 flex -translate-y-1/2 items-center gap-1">
                        {sttEnabled && settings.speechToText && <AudioRecorder disabled={isLoading} onTranscriptionComplete={handleTranscription} onError={handleTranscriptionError} onRecordingStateChange={setIsRecording} />}
                        <Button type="submit" disabled={!input.trim() || isLoading} variant="ghost" size="icon" tooltip="Send message">
                            {isLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
                        </Button>
                    </div>
                </form>
            </div>
        </div>
    );
};
