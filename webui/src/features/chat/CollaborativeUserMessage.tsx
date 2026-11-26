// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { MarkdownWrapper } from "@/common/markdown";

import type { MessageFE, TextPart } from "@/types";
import { ChatBubble, ChatBubbleMessage } from "@/ui";

import { MessageAttribution } from "./MessageAttribution";
import { MessageHoverButtons } from "./MessageHoverButtons";

interface CollaborativeUserMessageProps {
    readonly message: MessageFE;
    readonly userName: string;
    readonly timestamp: number;
    readonly userIndex?: number;
}

export function CollaborativeUserMessage({ message, userName, timestamp, userIndex }: CollaborativeUserMessageProps) {
    const textPart = message.parts?.find(p => p.kind === "text") as TextPart | undefined;
    const text = textPart?.text || "";

    return (
        <div className="flex flex-col items-start gap-1">
            <MessageAttribution type="user" name={userName} timestamp={timestamp} userIndex={userIndex} />
            <div className="ml-10 flex max-w-[70%] flex-col gap-1">
                <ChatBubble variant="other-user">
                    <ChatBubbleMessage variant="other-user">
                        <MarkdownWrapper content={text} />
                    </ChatBubbleMessage>
                </ChatBubble>
                <div className="flex justify-start">
                    <MessageHoverButtons message={message} />
                </div>
            </div>
        </div>
    );
}
