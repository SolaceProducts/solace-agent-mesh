// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentType, type ReactNode, useMemo, useState } from "react";

import { BuilderContext, type BuilderContextValue, type SelectedBuilderNode } from "@/providers/contexts/BuilderContext";

import { type BuilderCreationState, INITIAL_BUILDER_CREATION_STATE } from "@/types";

interface BuilderProviderProps {
    children: ReactNode;
    builderTabContent?: ComponentType<{ userAgentsRefetch: () => Promise<void> }> | null;
}

export function BuilderProvider({ children, builderTabContent = null }: BuilderProviderProps) {
    const [builderMode, setBuilderMode] = useState<boolean>(false);
    const [builderCreationState, setBuilderCreationState] = useState<BuilderCreationState>(INITIAL_BUILDER_CREATION_STATE);
    const [selectedBuilderNode, setSelectedBuilderNode] = useState<SelectedBuilderNode | null>(null);

    const value = useMemo<BuilderContextValue>(
        () => ({
            builderMode,
            setBuilderMode,
            builderCreationState,
            setBuilderCreationState,
            selectedBuilderNode,
            setSelectedBuilderNode,
            builderTabContent,
        }),
        [builderMode, builderCreationState, selectedBuilderNode, builderTabContent]
    );

    return <BuilderContext.Provider value={value}>{children}</BuilderContext.Provider>;
}
