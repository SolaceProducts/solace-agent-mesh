// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createContext, useContext } from "react";

import type { BuilderCreationState } from "@/types";

export interface SelectedBuilderNode {
    type: string;
    name: string;
}

export interface BuilderContextValue {
    builderMode: boolean;
    setBuilderMode: React.Dispatch<React.SetStateAction<boolean>>;

    builderCreationState: BuilderCreationState;
    setBuilderCreationState: React.Dispatch<React.SetStateAction<BuilderCreationState>>;

    selectedBuilderNode: SelectedBuilderNode | null;
    setSelectedBuilderNode: React.Dispatch<React.SetStateAction<SelectedBuilderNode | null>>;

    builderTabContent: React.ComponentType<{ userAgentsRefetch: () => Promise<void> }> | null;
}

export const BuilderContext = createContext<BuilderContextValue | undefined>(undefined);

export const BUILDER_AGENT_NAME = "Builder";

export const BUILDER_SESSION_LIST_EXCLUDE_AGENT_IDS = ["__builder_test__"];

export const BUILDER_AGENT_SESSION_ROUTES: Record<string, string> = { Builder: "/builder" };

export const useBuilderContext = (): BuilderContextValue => {
    const context = useContext(BuilderContext);
    if (context === undefined) {
        throw new Error("useBuilderContext must be used within a BuilderProvider");
    }
    return context;
};
