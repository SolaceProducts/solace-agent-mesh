// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ModelConfig } from "@/api/models/types";

import { cn } from "@/utils/cnTailwind";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { ModelConfigIcon, useFreeModel } from "@/features/activation";

interface AgentModelCardProps {
    model: ModelConfig;
    selected?: boolean;
    selectable?: boolean;
    onSelect?: () => void;
    className?: string;
}

const CardContent = ({ model }: { model: ModelConfig }) => {
    const freeModel = useFreeModel().freeModelLabel(model);
    return (
        <>
            <div className="flex min-w-0 flex-1 flex-col">
                <span className="text-sm font-semibold">{model.alias}</span>
                {model.description && (
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <span className="truncate text-sm text-(--secondary-text-wMain)">{model.description}</span>
                        </TooltipTrigger>
                        <TooltipContent>{model.description}</TooltipContent>
                    </Tooltip>
                )}
            </div>
            <div className="flex flex-shrink-0 items-center gap-2">
                <ModelConfigIcon model={model} size="xs" />
                <span className="text-xs text-(--secondary-text-wMain)">{freeModel?.modelName ?? model.modelName}</span>
            </div>
        </>
    );
};

export const AgentModelCard = ({ model, selected = false, selectable = false, onSelect, className }: AgentModelCardProps) => {
    if (selectable) {
        return (
            <label className={cn("flex cursor-pointer items-center gap-4 rounded-xs border p-4", selected && "border-(--brand-wMain)", className)}>
                <input type="radio" name="model-selection" className="sr-only" checked={selected} onChange={() => onSelect?.()} />
                <CardContent model={model} />
            </label>
        );
    }

    return (
        <div className={cn("flex items-center gap-4 rounded-sm border p-4", className)}>
            <CardContent model={model} />
        </div>
    );
};
