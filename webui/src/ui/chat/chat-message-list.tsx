// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useImperativeHandle } from "react";
import { ArrowDown } from "lucide-react";

import { Button } from "../button";
import { useAutoScroll } from "../hooks/useAutoScroll";

import { CHAT_STYLES } from "./chatStyles";

interface ChatMessageListProps extends React.HTMLAttributes<HTMLDivElement> {
    smooth?: boolean;
}
export interface ChatMessageListRef {
    scrollToBottom: () => void;
    scrollContainer: HTMLDivElement | null;
    pauseAutoScroll: () => Promise<void>;
}

const ChatMessageList = React.forwardRef<ChatMessageListRef, ChatMessageListProps>(({ className = "", children, ...props }, ref) => {
    const contentRef = React.useRef<HTMLDivElement>(null);
    const { scrollRef, isAtBottom, disableAutoScroll, scrollToBottom, pauseAutoScroll, userHasScrolled } = useAutoScroll({
        smooth: true,
        content: children,
        contentRef,
    });

    useImperativeHandle(ref, () => ({
        scrollToBottom,
        pauseAutoScroll,
        get scrollContainer() {
            return scrollRef.current;
        },
    }));

    return (
        <div className={`fade-both-mask relative h-full min-h-0 w-full min-w-0 flex-1 py-3 ${className}`}>
            <div
                className="flex h-full w-full min-w-0 flex-col overflow-y-auto p-4"
                ref={scrollRef}
                onWheel={disableAutoScroll}
                onTouchMove={disableAutoScroll}
                {...props}
                style={{
                    scrollBehavior: "smooth",
                }}
            >
                <div className="relative flex flex-col gap-8" style={CHAT_STYLES} ref={contentRef}>
                    {children}
                </div>
            </div>

            {!isAtBottom && userHasScrolled && scrollRef.current && scrollRef.current.scrollHeight > scrollRef.current.clientHeight + 100 && (
                <Button
                    onClick={() => {
                        scrollToBottom();
                    }}
                    size="icon"
                    variant="outline"
                    className="absolute bottom-2 left-1/2 z-20 inline-flex -translate-x-1/2 transform rounded-full bg-(--background-w10) shadow-md"
                    aria-label="Scroll to bottom"
                >
                    <ArrowDown className="h-4 w-4" />
                </Button>
            )}
        </div>
    );
});

ChatMessageList.displayName = "ChatMessageList";

export { ChatMessageList };
