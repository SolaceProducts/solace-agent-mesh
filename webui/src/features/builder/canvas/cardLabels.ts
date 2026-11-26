// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Node } from "@xyflow/react";

import { humanizeComponentName } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";

function isNonBlank(value: unknown): value is string {
    return typeof value === "string" && value.trim() !== "";
}

export function agentCardName(canvasData: ComponentCanvasData | null | undefined, componentName: string | undefined): string {
    return [canvasData?.agentConfig?.name, canvasData?.name, componentName].find(isNonBlank) ?? "Agent";
}

function humanized(value: unknown): string | undefined {
    return isNonBlank(value) ? humanizeComponentName(value) : undefined;
}

interface NamedData {
    component?: { name?: string };
    workflowComponent?: { name?: string };
    canvasData?: ComponentCanvasData | null;
    layoutNode?: { data?: { agentName?: unknown; label?: unknown } };
}

export function cardAccessibleName({ type, data }: Node): string | undefined {
    const named = data as NamedData;
    switch (type) {
        case "wf-agent": {
            const shown = named.layoutNode?.data?.agentName || named.layoutNode?.data?.label;
            return isNonBlank(shown) ? shown : undefined;
        }
        case "wf-start":
            return humanized(named.workflowComponent?.name);
        case "agent":
            return humanized(agentCardName(named.canvasData, named.component?.name));
        default:
            return humanized(named.component?.name);
    }
}
