// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { isComponentLiveOnMesh, isSessionComponentLive } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";

import { useCanvasView } from "./canvasViewContext";

export interface ComponentForStatus {
    id?: string;
    name?: string;
    type?: string;
    origin?: string;
    status?: string;
}

export interface DerivedCardStatus {
    isGenerating: boolean;
    isQueued: boolean;
    isWaitingForInput: boolean;
    isBuildingDeps: boolean;
    isIdle: boolean;
    isActive: boolean;
    isReady: boolean;
    isChangedSinceValidation: boolean;
    isDraft: boolean;
    isEditing: boolean;
    showsDraft: boolean;
    showsQueued: boolean;
    statusText: string | undefined;
    effectiveStatus: string | undefined;
}

export const WAITING_FOR_INPUT_TEXT = "Waiting for Input...";
export const PAUSED_TEXT = "Paused";
export const CHANGED_SINCE_VALIDATION_TEXT = "Changed since validation";

function derivePillFlags(args: { buildInterrupted: boolean; isQueued: boolean; isDraft: boolean; isWaitingForInput: boolean; isChangedSinceValidation: boolean }): Pick<DerivedCardStatus, "showsDraft" | "showsQueued" | "statusText"> {
    const isPaused = args.buildInterrupted && (args.isQueued || args.isDraft);
    const showsDraft = args.isDraft && !isPaused;
    const showsQueued = args.isQueued && !isPaused;
    if (args.isWaitingForInput) return { showsDraft, showsQueued, statusText: WAITING_FOR_INPUT_TEXT };
    if (isPaused) return { showsDraft, showsQueued, statusText: PAUSED_TEXT };
    return { showsDraft, showsQueued, statusText: args.isChangedSinceValidation ? CHANGED_SINCE_VALIDATION_TEXT : undefined };
}

function isEditedSinceValidation(isValidatedAndIdle: boolean, canvasData: ComponentCanvasData | null | undefined): boolean {
    return isValidatedAndIdle && (canvasData?.changedSinceValidation ?? false);
}

export function useDerivedCardStatus(canvasData: ComponentCanvasData | null | undefined, component?: ComponentForStatus): DerivedCardStatus {
    const { builderActiveTab, buildInterrupted, isApplyingChanges, liveAgentCards, liveConnectors } = useCanvasView();

    let baseStatus: string | undefined;
    if (component) {
        baseStatus = component.origin === "platform" ? "deployed" : component.status;
    } else {
        baseStatus = canvasData?.status;
    }
    const componentName = component?.name ?? canvasData?.name;
    const componentType = component?.type ?? canvasData?.type;
    const liveOnMesh = isSessionComponentLive({ id: component?.id ?? canvasData?.id, type: componentType }, liveAgentCards);
    let effectiveStatus = liveOnMesh ? "deployed" : baseStatus;
    const isConnector = componentType === "connector";
    const origin = component?.origin ?? canvasData?.origin;
    if (isConnector && effectiveStatus === "validated" && origin !== "platform" && liveConnectors && !isComponentLiveOnMesh(componentName ?? "", liveConnectors)) {
        effectiveStatus = "draft";
    }

    const isBuildFinalizing = (canvasData?.isBuildFinalizing ?? false) && effectiveStatus !== "deployed";
    const isGenerating = (canvasData?.isGenerating ?? false) || isBuildFinalizing;
    const isQueued = canvasData?.isQueued ?? false;
    const isWaitingForInput = canvasData?.isWaitingForInput ?? false;
    const isBuildingDeps = canvasData?.isBuildingDeps ?? false;
    const isIdle = !isGenerating && !isQueued && !isWaitingForInput && !isBuildingDeps;

    const isActive = isIdle && effectiveStatus === "deployed";
    const isValidatedAndIdle = isIdle && !isActive && effectiveStatus === "validated";
    const isChangedSinceValidation = isEditedSinceValidation(isValidatedAndIdle, canvasData);
    const isReady = isValidatedAndIdle && !isChangedSinceValidation;
    const isDraft = isIdle && !isActive && !isReady && origin !== "platform" && effectiveStatus !== "error";

    const isEditing = isGenerating && (effectiveStatus === "deployed" || isApplyingChanges);

    const { showsDraft, showsQueued, statusText } = derivePillFlags({ buildInterrupted, isQueued, isDraft, isWaitingForInput, isChangedSinceValidation });

    if (builderActiveTab === "test") {
        return {
            isGenerating: false,
            isQueued: false,
            isWaitingForInput: false,
            isBuildingDeps: false,
            isIdle: true,
            isActive: false,
            isReady: false,
            isChangedSinceValidation: false,
            isDraft: false,
            isEditing: false,
            showsDraft: false,
            showsQueued: false,
            statusText: undefined,
            effectiveStatus: undefined,
        };
    }

    return {
        isGenerating,
        isQueued,
        isWaitingForInput,
        isBuildingDeps,
        isIdle,
        isActive,
        isReady,
        isChangedSinceValidation,
        isDraft,
        isEditing,
        showsDraft,
        showsQueued,
        statusText,
        effectiveStatus,
    };
}
