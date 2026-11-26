// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { CSSProperties, ReactNode } from "react";

import { cn } from "@/utils/cnTailwind";

const DOT_CENTER_PX = 16;
const RAIL_LEFT = "left-[9px]";

export interface TimelineRowProps {
    dot: ReactNode;
    isFirst: boolean;
    isLast: boolean;
    railClassName: string;
    animationDelayMs?: number;
    children: ReactNode;
    contentClassName?: string;
}

export function TimelineRow({ dot, isFirst, isLast, railClassName, animationDelayMs = 0, children, contentClassName }: Readonly<TimelineRowProps>) {
    const style: CSSProperties = { animation: "progressSlideIn 0.3s ease-out both", animationDelay: `${animationDelayMs}ms` };
    return (
        <div className="relative flex items-start gap-3 py-1.5" style={style}>
            {!isFirst && <div className={cn("absolute top-0 z-0 w-[2px]", RAIL_LEFT, railClassName)} style={{ height: DOT_CENTER_PX }} />}
            {!isLast && <div className={cn("absolute bottom-0 z-0 w-[2px]", RAIL_LEFT, railClassName)} style={{ top: DOT_CENTER_PX }} />}
            {dot}
            <div className={cn("min-w-0 flex-1", contentClassName)}>{children}</div>
        </div>
    );
}
