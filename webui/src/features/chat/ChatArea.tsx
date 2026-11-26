// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment, useMemo, useRef } from "react";

import { useChatContext } from "@/providers/contexts";

import type { ChatMessageListRef } from "@/ui/chat/chat-message-list";

import { EmptyState, LoadingMessageRow } from "@/common";
import type { AgentWelcomeConfig, MessageFE } from "@/types";
import { CHAT_STYLES, ChatMessageList } from "@/ui";

import { ChatInputArea } from "./ChatInputArea";
import { ChatMessage } from "./ChatMessage";
import { chatAgentGate } from "./ChatPage.helpers";
import { ChatWelcomeScreen } from "./ChatWelcomeScreen";
import { useChatAgents, useInlineTimelineActive } from "./hooks";
import { MessageErrorBoundary } from "./MessageErrorBoundary";

export interface ChatAreaProps {
    compact?: boolean;
    hideWelcomeScreen?: boolean;
    allowAgentSelection?: boolean;
    className?: string;
    welcomeOverride?: AgentWelcomeConfig;
    renderMessageAddon?: (message: MessageFE, index: number) => React.ReactNode;
    messageFilter?: (message: MessageFE, index: number) => boolean;
    inputFormHeader?: React.ReactNode;
    hideInlineActivityForMessageId?: string;
    suppressLoadingIndicator?: boolean;
}

export const ChatArea = ({
    compact = false,
    hideWelcomeScreen = false,
    allowAgentSelection = true,
    className,
    welcomeOverride,
    renderMessageAddon,
    messageFilter,
    inputFormHeader,
    hideInlineActivityForMessageId,
    suppressLoadingIndicator = false,
}: ChatAreaProps) => {
    const chatMessageListRef = useRef<ChatMessageListRef>(null);
    const { messages, agents, isResponding, isLoadingSession, selectedAgentName } = useChatContext();

    const { isLoading: isLoadingAgents, isRefetching, isError: hasAgentsError, refetch } = useChatAgents();

    const filteredMessages = useMemo(() => {
        if (!messageFilter) return messages;
        return messages.filter((msg, idx) => messageFilter(msg, idx));
    }, [messages, messageFilter]);

    const lastMessageIndexByTaskId = useMemo(() => {
        const map = new Map<string, number>();
        filteredMessages.forEach((message, filteredIndex) => {
            if (message.taskId) {
                map.set(message.taskId, filteredIndex);
            }
        });
        return map;
    }, [filteredMessages]);

    const isWelcomeState = useMemo(() => {
        if (filteredMessages.length === 0) return true;
        if (filteredMessages.length === 1 && !filteredMessages[0].isUser && filteredMessages[0].metadata?.sessionId === "") return true;
        return false;
    }, [filteredMessages]);

    const inlineTimelineActive = useInlineTimelineActive(filteredMessages, hideInlineActivityForMessageId);

    const showWelcome = !hideWelcomeScreen && isWelcomeState && !isResponding;
    const paddingClass = compact ? "py-2" : "py-6";
    const textSizeClass = "text-base";

    let chatBody: React.ReactNode;
    if (isLoadingSession) {
        chatBody = <EmptyState variant="loading" title="Loading session" />;
    } else if (showWelcome) {
        chatBody = chatAgentGate({ isLoadingAgents, hasAgentsError, agents, agentsRefetch: { refetch, isRefetching } }) ?? (
            <ChatWelcomeScreen agents={agents} selectedAgentName={selectedAgentName} welcomeOverride={welcomeOverride} compact={compact} />
        );
    } else {
        chatBody = (
            <>
                <ChatMessageList className={textSizeClass} ref={chatMessageListRef}>
                    {filteredMessages.map((message, index) => {
                        const isLastWithTaskId = !!(message.taskId && lastMessageIndexByTaskId.get(message.taskId) === index);
                        const messageKey = `${message.metadata?.messageId || "temp"}-${index}`;
                        const isLastMessage = index === filteredMessages.length - 1;
                        const shouldStream = isLastMessage && isResponding && !message.isUser;
                        return (
                            <Fragment key={messageKey}>
                                <MessageErrorBoundary>
                                    <ChatMessage
                                        message={message}
                                        isLastWithTaskId={isLastWithTaskId}
                                        isStreaming={shouldStream}
                                        hideInlineActivityTimeline={!!hideInlineActivityForMessageId && message.metadata?.messageId === hideInlineActivityForMessageId}
                                    />
                                    {renderMessageAddon?.(message, index)}
                                </MessageErrorBoundary>
                            </Fragment>
                        );
                    })}
                    {isResponding && !suppressLoadingIndicator && !inlineTimelineActive && <LoadingMessageRow />}
                </ChatMessageList>
                <div style={CHAT_STYLES}>
                    <ChatInputArea agents={agents} scrollToBottom={chatMessageListRef.current?.scrollToBottom} allowAgentSelection={allowAgentSelection} formHeader={inputFormHeader} />
                </div>
            </>
        );
    }

    return (
        <div className={`flex h-full w-full min-w-0 flex-col ${className ?? ""}`}>
            <div className={`flex min-h-0 min-w-0 flex-1 flex-col ${paddingClass}`}>{chatBody}</div>
        </div>
    );
};
