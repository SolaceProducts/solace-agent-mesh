// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, type ReactNode, useContext, useMemo } from "react";

import type { BuilderActiveTab } from "../useBuilderPageState";

interface CanvasViewContextValue {
    builderActiveTab: BuilderActiveTab;
    testTargetName: string | null;
    onSelectTestTarget: (name: string) => void;
    isApplyingChanges: boolean;
    buildInterrupted: boolean;
    liveAgentCards?: ReadonlyArray<{ name?: string; displayName?: string }>;
    liveConnectors?: ReadonlyArray<{ name?: string }>;
}

const CanvasViewContext = createContext<CanvasViewContextValue>({
    builderActiveTab: "build",
    testTargetName: null,
    onSelectTestTarget: () => {},
    isApplyingChanges: false,
    buildInterrupted: false,
    liveAgentCards: undefined,
    liveConnectors: undefined,
});

type CanvasViewProviderProps = Omit<CanvasViewContextValue, "buildInterrupted"> & { buildInterrupted?: boolean; children: ReactNode };

export function CanvasViewProvider({ builderActiveTab, testTargetName, onSelectTestTarget, isApplyingChanges, buildInterrupted = false, liveAgentCards, liveConnectors, children }: CanvasViewProviderProps) {
    const value = useMemo(
        () => ({ builderActiveTab, testTargetName, onSelectTestTarget, isApplyingChanges, buildInterrupted, liveAgentCards, liveConnectors }),
        [builderActiveTab, testTargetName, onSelectTestTarget, isApplyingChanges, buildInterrupted, liveAgentCards, liveConnectors]
    );
    return <CanvasViewContext.Provider value={value}>{children}</CanvasViewContext.Provider>;
}

export function useCanvasView(): CanvasViewContextValue {
    return useContext(CanvasViewContext);
}
