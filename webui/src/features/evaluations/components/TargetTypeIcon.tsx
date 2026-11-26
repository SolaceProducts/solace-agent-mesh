// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Bot, Workflow } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { LifecycleBadge } from "@/ui/lifecycleBadge";
import type { EvalTargetType } from "@/types/eval";

import { useIsWorkflowEvalsEnabled } from "@/hooks";

export function TargetTypeIcon({ targetType, className }: { readonly targetType: EvalTargetType | undefined; readonly className?: string }) {
    const workflowEvalsEnabled = useIsWorkflowEvalsEnabled();
    const isWorkflow = targetType === "workflow";
    if (!isWorkflow && !workflowEvalsEnabled) return null;
    const Icon = isWorkflow ? Workflow : Bot;
    const label = isWorkflow ? "Workflow" : "Agent";
    return <Icon role="img" aria-label={label} className={cn("size-5 shrink-0 text-(--secondary-text-wMain)", className)} data-testid="targetTypeIcon" data-target-type={isWorkflow ? "workflow" : "agent"} />;
}

export function WorkflowEarlyAccessBadge({ targetType }: { readonly targetType: EvalTargetType | undefined }) {
    return targetType === "workflow" ? <LifecycleBadge>EARLY ACCESS</LifecycleBadge> : null;
}
