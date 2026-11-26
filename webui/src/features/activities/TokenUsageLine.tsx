// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { ArrowDown, ArrowUp } from "lucide-react";

import { cn, formatTokenCount } from "@/utils";

import { formatTokenUsageTitle } from "./format";
import type { TokenUsage } from "./sessionGrouping";

interface TokenUsageLineProps {
    usage: TokenUsage;
    className?: string;
    testId: string;
}

export const TokenUsageLine: React.FC<TokenUsageLineProps> = ({ usage, className, testId }) => (
    <p className={cn("flex items-center justify-end gap-2 text-xs", className)} data-testid={testId} title={formatTokenUsageTitle(usage)}>
        <span className="flex items-center gap-0.5">
            <ArrowUp className="h-3 w-3" aria-hidden />
            {formatTokenCount(usage.input)}
        </span>
        <span className="flex items-center gap-0.5">
            <ArrowDown className="h-3 w-3" aria-hidden />
            {formatTokenCount(usage.output)}
        </span>
    </p>
);
