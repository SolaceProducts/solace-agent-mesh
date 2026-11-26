// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Loader2, Send, Sparkles } from "lucide-react";

import { api } from "@/api/client";

import { useAudioSettings, useChatContext, useConfigContext } from "@/providers/contexts";

import { AudioRecorder } from "@/common/audio";
import { InlineNotice } from "@/common/banners/InlineNotice";
import { MessageBanner } from "@/common/banners/MessageBanner";
import { MarkdownWrapper } from "@/common/markdown";
import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";
import { BreathingDot } from "@/common/status";

import { getErrorMessage } from "@/utils/api";
import { uuid } from "@/utils/uuid";

import { Button } from "@/ui/button";
import { Textarea } from "@/ui/textarea";

import { cn, isImeComposing } from "@/utils";

import { AgentPickerCard, type AgentPickerSuggestion } from "./AgentPickerCard";
import { type AgentForBuilder, type AvailableAgentInput, projectAgentsForBuilder } from "./projectAgentsForBuilder";

interface InlineAgentPicker {
    type: "agent_picker";
    prompt: string;
    suggestions: AgentPickerSuggestion[];
    allowOther: boolean;
}

interface Message {
    id: string;
    role: "user" | "assistant";
    content: string;
    timestamp: Date;
    inlineComponent?: InlineAgentPicker;
    resolvedAgentName?: string;
    isError?: boolean;
    isWarning?: boolean;
}

function messageBubbleClass(message: Message): string {
    if (message.isError) return "max-w-[80%]";
    if (message.inlineComponent) return "w-full py-3";
    return "max-w-[80%] rounded-2xl px-4 py-3";
}

function MessageBody({ message }: Readonly<{ message: Message }>) {
    if (message.isError) {
        return (
            <InlineNotice boxed variant={message.isWarning ? "warning" : "error"} className="text-sm">
                {message.content}
            </InlineNotice>
        );
    }
    if (message.role === "assistant") {
        return <MarkdownWrapper content={message.content} className="text-sm leading-relaxed" />;
    }
    return <div className="text-sm leading-relaxed whitespace-pre-wrap">{message.content}</div>;
}

interface ChatResponse {
    message: string;
    taskUpdates: Record<string, unknown>;
    confidence: number;
    readyToSave: boolean;
    inlineComponent?: InlineAgentPicker;
    isError?: boolean;
    isWarning?: boolean;
}

interface ApiInlineComponent {
    type: string;
    prompt?: string;
    suggestions?: Array<{ name: string; reason?: string }>;
    allowOther?: boolean;
    allow_other?: boolean;
}

interface ApiChatResponse {
    message: string;
    taskUpdates: Record<string, unknown>;
    confidence: number;
    readyToSave: boolean;
    inlineComponent?: ApiInlineComponent | null;
    isError?: boolean;
    isWarning?: boolean;
}

function transformChatResponse(apiResponse: ApiChatResponse): ChatResponse {
    let inlineComponent: InlineAgentPicker | undefined;
    const raw = apiResponse.inlineComponent;
    if (raw && raw.type === "agent_picker" && Array.isArray(raw.suggestions) && raw.suggestions.length > 0) {
        inlineComponent = {
            type: "agent_picker",
            prompt: raw.prompt || "What agent would you like to use?",
            suggestions: raw.suggestions.map((s: { name: string; reason?: string }) => ({ name: s.name, reason: s.reason ?? "" })),
            allowOther: raw.allowOther ?? raw.allow_other ?? true,
        };
    }
    return {
        message: apiResponse.message,
        taskUpdates: apiResponse.taskUpdates,
        confidence: apiResponse.confidence,
        readyToSave: apiResponse.readyToSave,
        inlineComponent,
        isError: apiResponse.isError,
        isWarning: apiResponse.isWarning,
    };
}

interface TaskConfig {
    name?: string;
    description?: string;
    scheduleType?: string;
    scheduleExpression?: string;
    targetAgentName?: string;
    taskMessage?: string;
    timezone?: string;
}

interface TaskBuilderChatProps {
    onConfigUpdate: (config: Partial<TaskConfig>) => void;
    currentConfig: TaskConfig;
    initialMessage?: string | null;
    availableAgents?: AvailableAgentInput[];
    isEditing?: boolean;
}

export const TaskBuilderChat: React.FC<TaskBuilderChatProps> = ({ onConfigUpdate, currentConfig, initialMessage, availableAgents = [], isEditing = false }) => {
    const { addNotification } = useChatContext();
    const [messages, setMessages] = useState<Message[]>([]);
    const [input, setInput] = useState("");
    const [hasUserMessage, setHasUserMessage] = useState(false);
    const [isSending, setIsSending] = useState(false);
    const messagesEndRef = useRef<HTMLDivElement>(null);
    const inputRef = useRef<HTMLTextAreaElement>(null);
    const initRef = useRef(false);
    const currentConfigRef = useRef(currentConfig);
    currentConfigRef.current = currentConfig;
    const availableAgentsRef = useRef(availableAgents);
    availableAgentsRef.current = availableAgents;
    const onConfigUpdateRef = useRef(onConfigUpdate);
    onConfigUpdateRef.current = onConfigUpdate;
    const addNotificationRef = useRef(addNotification);
    addNotificationRef.current = addNotification;

    const { settings } = useAudioSettings();
    const { speechToTextEnabled } = useConfigContext();
    const sttEnabled = Boolean(speechToTextEnabled);
    const [sttError, setSttError] = useState<string | null>(null);
    const [isRecording, setIsRecording] = useState(false);

    const greetingQuery = useQuery({
        queryKey: ["scheduled-tasks", "builder", "greeting"],
        queryFn: () => api.webui.get("/api/v1/scheduledTasks/builder/greeting") as Promise<{ message: string }>,
        staleTime: Infinity,
        refetchOnMount: false,
    });

    const chatMutation = useMutation({
        mutationFn: (payload: { message: string; conversationHistory: Array<{ role: string; content: string }>; currentTask: TaskConfig; availableAgents: AgentForBuilder[] }) =>
            api.webui.post("/api/v1/scheduledTasks/builder/chat", payload) as Promise<ApiChatResponse>,
        onMutate: () => {
            setIsSending(true);
        },
        onSuccess: apiData => {
            const data = transformChatResponse(apiData);
            setMessages(prev => [...prev, { id: uuid(), role: "assistant", content: data.message, timestamp: new Date(), inlineComponent: data.inlineComponent, isError: data.isError, isWarning: data.isWarning }]);
            if (!data.isError && Object.keys(data.taskUpdates).length > 0) {
                onConfigUpdateRef.current(data.taskUpdates);
            }
        },
        onError: error => {
            reportAndNotifyError(addNotificationRef.current, getErrorMessage(error, "Failed to send message"), "api");
            setMessages(prev => [...prev, { id: uuid(), role: "assistant", content: "I encountered an error. Could you please try again?", timestamp: new Date(), isError: true }]);
        },
        onSettled: () => {
            setIsSending(false);
            setTimeout(() => inputRef.current?.focus(), 100);
        },
    });

    const scrollToBottom = () => {
        messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
    };

    useEffect(() => {
        scrollToBottom();
    }, [messages]);

    const greetingTextRef = useRef<string>("");
    useEffect(() => {
        if (initRef.current || !greetingQuery.data) return;
        initRef.current = true;

        const taskName = currentConfigRef.current?.name?.trim();
        const greetingMessage = isEditing
            ? `Hi! Let's refine${taskName ? ` the **${taskName}**` : " this"} task. Tell me what you'd like to change — for example the schedule, target agent, or the instructions. I can also help tweak the wording.`
            : greetingQuery.data.message;
        greetingTextRef.current = greetingMessage;
        setMessages([
            {
                id: uuid(),
                role: "assistant",
                content: greetingMessage,
                timestamp: new Date(),
            },
        ]);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [greetingQuery.data]);

    const initialMessageSentRef = useRef(false);
    useEffect(() => {
        if (!initialMessage || initialMessageSentRef.current) return;
        if (!greetingTextRef.current) return;

        const send = () => {
            if (initialMessageSentRef.current) return;
            initialMessageSentRef.current = true;
            setHasUserMessage(true);
            const userMessage: Message = {
                id: uuid(),
                role: "user",
                content: initialMessage,
                timestamp: new Date(),
            };
            setMessages(prev => [...prev, userMessage]);
            setTimeout(() => scrollToBottom(), 100);

            chatMutation.mutate({
                message: initialMessage,
                conversationHistory: [{ role: "assistant", content: greetingTextRef.current }],
                currentTask: currentConfigRef.current,
                availableAgents: projectAgentsForBuilder(availableAgentsRef.current),
            });
        };

        if (availableAgents.length > 0) {
            send();
            return;
        }
        const id = setTimeout(send, 3_000);
        return () => clearTimeout(id);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [initialMessage, greetingQuery.data, availableAgents.length]);

    useEffect(() => {
        if (greetingQuery.error && !initRef.current) {
            initRef.current = true;
            reportAndNotifyError(addNotification, getErrorMessage(greetingQuery.error, "Failed to initialize chat"), "api");
            setMessages([
                {
                    id: uuid(),
                    role: "assistant",
                    content: "Hi! I'll help you create a scheduled task. What would you like to automate?",
                    timestamp: new Date(),
                },
            ]);
        }
    }, [greetingQuery.error, addNotification]);

    const isInitializing = greetingQuery.isLoading;
    const isLoading = isSending;

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

    const sendMessage = (text: string) => {
        const trimmed = text.trim();
        if (!trimmed || isLoading) return;

        const userMessage: Message = {
            id: uuid(),
            role: "user",
            content: trimmed,
            timestamp: new Date(),
        };

        setMessages(prev => [...prev, userMessage]);
        setHasUserMessage(true);
        initialMessageSentRef.current = true;

        chatMutation.mutate({
            message: userMessage.content,
            conversationHistory: messages.filter(m => !m.isError && m.content && m.content.trim().length > 0).map(m => ({ role: m.role, content: m.content })),
            currentTask: currentConfig,
            availableAgents: projectAgentsForBuilder(availableAgents),
        });
    };

    const handleSend = () => {
        if (!input.trim() || isLoading) return;
        const text = input.trim();
        setInput("");
        sendMessage(text);
    };

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        handleSend();
    };

    const handleAgentPickerSelect = (messageId: string, agentName: string) => {
        if (isLoading) return;
        const agent = availableAgents.find(a => a.name === agentName);
        const displayName = agent?.displayName || agentName;
        setMessages(prev => prev.map(m => (m.id === messageId ? { ...m, resolvedAgentName: agentName } : m)));
        onConfigUpdate({ targetAgentName: agentName });
        sendMessage(`Use ${displayName}.`);
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
                {messages.map(message => (
                    <div key={message.id} className={cn("flex", message.role === "user" ? "justify-end" : "justify-start")}>
                        <div className={cn(messageBubbleClass(message), message.role === "user" && "bg-(--secondary-w20)")}>
                            <MessageBody message={message} />
                            {message.inlineComponent?.type === "agent_picker" && (
                                <AgentPickerCard
                                    prompt={message.inlineComponent.prompt}
                                    suggestions={message.inlineComponent.suggestions}
                                    allowOther={message.inlineComponent.allowOther}
                                    availableAgents={availableAgents}
                                    resolvedAgentName={message.resolvedAgentName}
                                    onSelect={agentName => handleAgentPickerSelect(message.id, agentName)}
                                />
                            )}
                        </div>
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
                        placeholder={isRecording ? "Recording..." : hasUserMessage ? "Type your message..." : "Describe what you want to automate..."}
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
