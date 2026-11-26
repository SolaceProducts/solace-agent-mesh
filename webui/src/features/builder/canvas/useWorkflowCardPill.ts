// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";

import { useCanvasView } from "./canvasViewContext";
import type { CardShellProps } from "./CardShell";
import { CHANGED_SINCE_VALIDATION_TEXT, type ComponentForStatus, type DerivedCardStatus, PAUSED_TEXT, useDerivedCardStatus, WAITING_FOR_INPUT_TEXT } from "./useDerivedCardStatus";

export type WorkflowCardPill = Required<Pick<CardShellProps, "generating" | "editing" | "queued" | "buildingDeps" | "ready" | "active" | "draft" | "error">> & Pick<CardShellProps, "statusText">;

export interface WorkflowCardPillExtras {
    buildInterrupted: boolean;
    depWaitingForInput?: boolean;
    pendingChildren?: { builderResponding: boolean };
}

function workflowStatusText(d: DerivedCardStatus, waiting: boolean, paused: boolean): string | undefined {
    if (waiting) return WAITING_FOR_INPUT_TEXT;
    if (paused) return PAUSED_TEXT;
    return d.isChangedSinceValidation ? CHANGED_SINCE_VALIDATION_TEXT : undefined;
}

export function deriveWorkflowCardPill(d: DerivedCardStatus, x: WorkflowCardPillExtras): WorkflowCardPill {
    const waiting = d.isWaitingForInput || !!x.depWaitingForInput;
    const settled = !waiting && !d.isBuildingDeps;
    const done = d.isActive || d.isReady;
    const childrenPending = done && !!x.pendingChildren;
    const childrenBuilding = childrenPending && !!x.pendingChildren?.builderResponding;
    const childrenStalled = childrenPending && !childrenBuilding && x.buildInterrupted;
    const paused = (x.buildInterrupted && (d.isQueued || d.isDraft)) || childrenStalled;
    const finished = done && settled && !childrenBuilding && !childrenStalled;
    const statusText = workflowStatusText(d, waiting, paused);

    return {
        generating: d.isGenerating || childrenBuilding,
        editing: d.isEditing || (childrenBuilding && d.isActive),
        queued: d.isQueued && settled && !x.buildInterrupted,
        buildingDeps: d.isBuildingDeps && !waiting,
        ready: finished && d.isReady,
        active: finished && d.isActive,
        draft: d.isDraft && settled && !x.buildInterrupted,
        error: d.effectiveStatus === "error" && !d.isGenerating && !waiting,
        statusText,
    };
}

export function useWorkflowCardPill(canvasData: ComponentCanvasData | null | undefined, component: ComponentForStatus, extras: Omit<WorkflowCardPillExtras, "buildInterrupted">): WorkflowCardPill {
    const derived = useDerivedCardStatus(canvasData, component);
    const { builderActiveTab, buildInterrupted } = useCanvasView();
    const inTestMode = builderActiveTab === "test";
    return deriveWorkflowCardPill(derived, { ...extras, buildInterrupted, depWaitingForInput: extras.depWaitingForInput && !inTestMode });
}
