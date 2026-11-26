// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ChevronRight } from "lucide-react";
import type { ReactNode } from "react";

import { ActivityDot } from "@/common/status";

import { cn } from "@/utils/cnTailwind";

export const HOVER_REVEAL = "opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100";

interface LiveActivityLineProps {
    text: string;
    animationKey?: string | number;
    onExpand: () => void;
    children?: ReactNode;
    className?: string;
}

export const LiveActivityLine = ({ text, animationKey, onExpand, children, className }: Readonly<LiveActivityLineProps>) => (
    <div className={cn("group flex items-center gap-2", className)}>
        <button type="button" onClick={onExpand} className="flex min-w-0 items-center gap-2 text-left" title="Show all activity steps" aria-expanded={false}>
            <ActivityDot />
            <span key={animationKey ?? text} className="min-w-0" style={{ animation: "progressSlideIn 0.35s ease-out" }}>
                <span className="thinking-shimmer block truncate text-sm font-medium" title={text}>
                    {text}
                </span>
            </span>
            <ChevronRight className="h-3.5 w-3.5 flex-shrink-0 text-(--secondary-text-wMain) opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100" />
        </button>
        {children}
    </div>
);
