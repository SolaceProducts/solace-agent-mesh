// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { EvalExperiment } from "@/types/eval";

import { MessageBanner } from "@/common";

import { ExperimentLink } from "./ExperimentLink";

interface ExperimentUsageBannerProps {
    resourceLabel: string;
    experiments: EvalExperiment[];
}

export function ExperimentUsageBanner({ resourceLabel, experiments }: ExperimentUsageBannerProps) {
    const count = experiments.length;
    return (
        <MessageBanner
            variant="info"
            message={
                <span>
                    This {resourceLabel} is used in{" "}
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <span className="cursor-pointer font-semibold underline underline-offset-2">
                                {count} {count === 1 ? "experiment" : "experiments"}
                            </span>
                        </TooltipTrigger>
                        <TooltipContent side="bottom" align="start" sideOffset={8} className="max-w-xs rounded-md border bg-(--background-w10) p-2 text-(--primary-text-wMain) shadow-md [&>span]:hidden">
                            <div className="flex flex-col gap-1">
                                {experiments.map(ex => (
                                    <ExperimentLink key={ex.id} id={ex.id} name={ex.name} />
                                ))}
                            </div>
                        </TooltipContent>
                    </Tooltip>
                    . Any changes made will be applied to future runs of these experiments. This will not impact previous runs.
                </span>
            }
        />
    );
}
