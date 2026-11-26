// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

"use client";

import * as React from "react";

import { cn } from "@/utils";

import { Tooltip, TooltipContent, TooltipTrigger } from "./tooltip";

interface TruncatedTextProps {
    text: string;
    children?: React.ReactNode;
    className?: string;
    tooltipClassName?: string;
    side?: React.ComponentProps<typeof TooltipContent>["side"];
}

export function TruncatedText({ text, children, className, tooltipClassName, side }: TruncatedTextProps) {
    const [isTruncated, setIsTruncated] = React.useState(false);
    const observerRef = React.useRef<ResizeObserver | null>(null);

    const measureRef = React.useCallback((node: HTMLSpanElement | null) => {
        observerRef.current?.disconnect();
        if (!node) return;
        const measure = () => setIsTruncated(node.scrollWidth > node.clientWidth);
        measure();
        observerRef.current = new ResizeObserver(measure);
        observerRef.current.observe(node);
    }, []);

    const content = (
        <span ref={measureRef} className={cn("truncate", className)}>
            {children ?? text}
        </span>
    );

    if (!isTruncated) return content;

    return (
        <Tooltip>
            <TooltipTrigger asChild>{content}</TooltipTrigger>
            <TooltipContent side={side} className={tooltipClassName}>
                {text}
            </TooltipContent>
        </Tooltip>
    );
}
