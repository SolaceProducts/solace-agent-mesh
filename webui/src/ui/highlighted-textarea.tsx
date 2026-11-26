// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";

import { cn } from "@/utils";

interface HighlightedTextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {
    highlightPattern?: RegExp;
    highlightClassName?: string;
}

const DEFAULT_HIGHLIGHT_PATTERN = /(\{\{[^{}]*\}\})/g;

const HighlightedTextarea = React.forwardRef<HTMLTextAreaElement, HighlightedTextareaProps>(
    ({ className, value, highlightPattern = DEFAULT_HIGHLIGHT_PATTERN, highlightClassName = "bg-(--info-w100) text-(--info-wMain) rounded-sm", ...props }, ref) => {
        const backdropRef = React.useRef<HTMLDivElement>(null);
        const textareaRef = React.useRef<HTMLTextAreaElement>(null);

        React.useImperativeHandle(ref, () => textareaRef.current!);

        const handleScroll = React.useCallback(() => {
            if (backdropRef.current && textareaRef.current) {
                backdropRef.current.scrollTop = textareaRef.current.scrollTop;
                backdropRef.current.scrollLeft = textareaRef.current.scrollLeft;
            }
        }, []);

        const highlightText = React.useCallback(
            (text: string) => {
                if (!text) return null;

                const parts = text.split(highlightPattern);
                const offsets = parts.map((_, i, arr) => arr.slice(0, i).reduce((sum, p) => sum + p.length, 0));
                return parts.map((part, index) => {
                    const key = `${offsets[index]}-${part.length}`;
                    return index % 2 === 1 ? (
                        <mark key={key} className={highlightClassName}>
                            {part}
                        </mark>
                    ) : (
                        <span key={key}>{part}</span>
                    );
                });
            },
            [highlightPattern, highlightClassName]
        );

        const textValue = typeof value === "string" ? value : "";

        return (
            <div className="relative">
                <div
                    ref={backdropRef}
                    className={cn("pointer-events-none absolute inset-0 overflow-hidden break-words whitespace-pre-wrap", "rounded-md border border-transparent px-3 py-2", "font-mono text-sm", className)}
                    style={{
                        lineHeight: "1.5",
                        wordWrap: "break-word",
                        overflowWrap: "break-word",
                    }}
                    aria-hidden="true"
                >
                    {highlightText(textValue)}
                    <br />
                </div>

                <textarea
                    ref={textareaRef}
                    className={cn("relative z-10 flex min-h-[80px] w-full rounded-md border bg-transparent px-3 py-2", "placeholder:opacity-75 disabled:cursor-not-allowed disabled:opacity-50", "font-mono text-sm", "text-transparent", className)}
                    style={{
                        lineHeight: "1.5",
                        caretColor: "var(--primary-text-wMain)",
                        WebkitTextFillColor: "transparent",
                    }}
                    value={value}
                    onScroll={handleScroll}
                    {...props}
                />
            </div>
        );
    }
);

HighlightedTextarea.displayName = "HighlightedTextarea";

export { HighlightedTextarea };
export type { HighlightedTextareaProps };
