// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { cn } from "@/utils";

interface HintedTextProps {
    children: ReactNode;
    hint: string;
    className?: string;
}

export function HintedText({ children, hint, className }: Readonly<HintedTextProps>) {
    return (
        <span>
            <Tooltip>
                <TooltipTrigger asChild>
                    <span tabIndex={0} className={cn("cursor-help underline decoration-dotted underline-offset-2", className)}>
                        {children}
                    </span>
                </TooltipTrigger>
                <TooltipContent className="whitespace-pre-line">{hint}</TooltipContent>
            </Tooltip>
            <span className="sr-only"> ({hint})</span>
        </span>
    );
}
