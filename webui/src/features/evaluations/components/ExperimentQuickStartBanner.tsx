// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { X } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { EvalFlowExplainer } from "./EvalFlowExplainer";

interface ExperimentQuickStartBannerProps {
    readonly onCreateExperiment?: () => void;
    readonly heading?: string;
    readonly description?: string;
    readonly quickStartTitle?: string;
    readonly quickStartDescription?: string;
    readonly ctaLabel?: string;
    readonly ctaDisabled?: boolean;
    readonly ctaTestId?: string;
    readonly onDismiss?: () => void;
}

const DEFAULT_HEADING = "Evaluate your agent mesh's performance";
const DEFAULT_DESCRIPTION = "Experiments validate that your agents respond consistently and safely allowing you to coach your agent in the correct behaviour before you ship and catch regressions whenever a prompt, model, or tool changes.";

export function ExperimentQuickStartBanner({
    onCreateExperiment,
    heading = DEFAULT_HEADING,
    description = DEFAULT_DESCRIPTION,
    quickStartTitle = "Create your first experiment",
    quickStartDescription = "Select the agent and models you want to evaluate, along with the criteria you want to evaluate against using a combination of a dataset and evaluators.",
    ctaLabel = "Create Experiment",
    ctaDisabled = false,
    ctaTestId = "createExperimentBanner",
    onDismiss,
}: ExperimentQuickStartBannerProps) {
    return (
        <div data-testid="experimentQuickStartBanner" className="relative flex flex-col items-stretch gap-6 rounded-xs border bg-(--secondary-w10)/40 p-6 sm:flex-row">
            <div className="flex flex-1 flex-col items-center gap-4 text-center sm:flex-[3] sm:basis-0 sm:flex-row sm:items-center sm:gap-5 sm:text-left">
                <EvalFlowExplainer variant="compact" className="shrink-0" />
                <div className="flex min-w-0 flex-col gap-1">
                    <h3 className="text-base font-semibold">{heading}</h3>
                    <p className="text-sm">{description}</p>
                </div>
            </div>
            <div className={cn("flex w-full shrink-0 flex-col gap-2 border-t pt-6 sm:flex-[2] sm:basis-0 sm:border-t-0 sm:border-l sm:pt-0 sm:pl-6", onDismiss && "sm:pr-8")}>
                <span className="text-xs font-semibold tracking-wide text-(--brand-wMain) uppercase">Quick Start</span>
                <span className="text-sm font-semibold">{quickStartTitle}</span>
                <p className="text-sm">{quickStartDescription}</p>
                <div className="mt-1 flex justify-end">
                    <Button data-testid={ctaTestId} disabled={ctaDisabled} onClick={onCreateExperiment}>
                        {ctaLabel}
                    </Button>
                </div>
            </div>
            {onDismiss && (
                <Tooltip>
                    <TooltipTrigger asChild>
                        <Button variant="ghost" size="icon" onClick={onDismiss} aria-label="Dismiss" data-testid="experimentQuickStartBannerDismiss" className="absolute top-2 right-2 size-7">
                            <X className="size-4" />
                        </Button>
                    </TooltipTrigger>
                    <TooltipContent>Close</TooltipContent>
                </Tooltip>
            )}
        </div>
    );
}
