// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";

import { cn } from "@/utils";

type CodeEditorProps = React.TextareaHTMLAttributes<HTMLTextAreaElement>;

const CodeEditor = React.forwardRef<HTMLTextAreaElement, CodeEditorProps>(({ className, value, onScroll, ...props }, ref) => {
    const gutterRef = React.useRef<HTMLDivElement>(null);
    const textareaRef = React.useRef<HTMLTextAreaElement>(null);

    React.useImperativeHandle(ref, () => textareaRef.current!);

    const text = typeof value === "string" ? value : "";
    const lineCount = Math.max(1, text.split("\n").length);

    const handleScroll = (event: React.UIEvent<HTMLTextAreaElement>) => {
        if (gutterRef.current) {
            gutterRef.current.scrollTop = event.currentTarget.scrollTop;
        }
        onScroll?.(event);
    };

    return (
        <div className={cn("relative flex overflow-hidden rounded-md border bg-transparent font-mono text-[13px] leading-5", className)}>
            <div ref={gutterRef} aria-hidden="true" className="pointer-events-none shrink-0 overflow-hidden border-r border-(--secondary-w20) bg-(--background-w20) py-2 pr-2 pl-3 text-right text-(--secondary-text-wMain) select-none">
                {Array.from({ length: lineCount }, (_, index) => (
                    <div key={index}>{index + 1}</div>
                ))}
            </div>
            <textarea
                ref={textareaRef}
                value={value}
                onScroll={handleScroll}
                spellCheck={false}
                wrap="off"
                className="min-h-0 w-full flex-1 resize-none overflow-auto border-0 bg-transparent py-2 pr-3 pl-2 whitespace-pre text-(--primary-text-wMain) outline-none placeholder:opacity-75 disabled:cursor-not-allowed disabled:opacity-50"
                {...props}
            />
        </div>
    );
});

CodeEditor.displayName = "CodeEditor";

export { CodeEditor };
export type { CodeEditorProps };
