// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { FlaskConical } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { useCanvasView } from "./canvasViewContext";

interface TestTargetButtonProps {
    componentName: string;
    eligible?: boolean;
    disabledWhenSelected?: boolean;
}

export function TestTargetButton({ componentName, eligible = true, disabledWhenSelected = false }: Readonly<TestTargetButtonProps>) {
    const { builderActiveTab, testTargetName, onSelectTestTarget } = useCanvasView();
    if (builderActiveTab !== "test" || !eligible) return null;
    const isSelected = testTargetName === componentName;
    let tooltipText = "Test this";
    if (disabledWhenSelected) tooltipText = "This node is already selected";
    else if (isSelected) tooltipText = "Already the active test target";
    return (
        <Button
            variant="ghost"
            size="sm"
            onClick={e => {
                e.stopPropagation();
                onSelectTestTarget(componentName);
            }}
            disabled={disabledWhenSelected}
            className={cn("nodrag nopan h-7 w-7 shrink-0 p-0 disabled:!cursor-default", isSelected ? "text-(--primary-wMain) hover:text-(--primary-wMain)" : "text-(--secondary-text-wMain) hover:text-(--primary-wMain)")}
            tooltip={tooltipText}
        >
            <FlaskConical className="h-4 w-4" />
        </Button>
    );
}
