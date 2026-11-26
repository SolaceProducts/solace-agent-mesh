// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { AlertTriangle } from "lucide-react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import type { RuntimeProblem } from "@/types";

interface HealthProblemsIndicatorProps {
    problems?: RuntimeProblem[];
    testIdPrefix?: string;
}

export const HealthProblemsIndicator = ({ problems, testIdPrefix = "healthProblems" }: HealthProblemsIndicatorProps) => {
    if (!problems || problems.length === 0) return null;

    const severity = problems.some(p => p.severity === "fatal") ? "fatal" : "degraded";
    const colourClass = severity === "fatal" ? "text-(--error-wMain)" : "text-(--warning-wMain)";
    const label = problems.length === 1 ? "1 issue" : `${problems.length} issues`;

    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <output data-testid={`${testIdPrefix}-indicator`} className={`inline-flex shrink-0 items-center gap-1 text-xs font-semibold whitespace-nowrap ${colourClass}`} tabIndex={0} onClick={e => e.stopPropagation()}>
                    <AlertTriangle className="size-4" />
                    {label}
                </output>
            </TooltipTrigger>
            <TooltipContent>
                <ul className="flex flex-col gap-1" data-testid={`${testIdPrefix}-details`}>
                    {problems.map(p => (
                        <li key={p.id}>{p.detail}</li>
                    ))}
                </ul>
            </TooltipContent>
        </Tooltip>
    );
};
