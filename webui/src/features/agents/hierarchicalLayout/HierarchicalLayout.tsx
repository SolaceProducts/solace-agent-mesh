// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import "@xyflow/react/dist/style.css";

import { ReactFlowProvider } from "@xyflow/react";

import { LayoutType } from "@/types/a2ui";
import { type AgentCardInfoFE } from "@/types/agent";

import { LayoutSelector } from "@/common";

import { AgentMeshFlowRenderer } from "./AgentMeshFlowRenderer";

interface HierarchicalLayoutProps {
    agents: AgentCardInfoFE[];
    currentLayout?: string;
    onLayoutChange?: (layout: string) => void;
}

export const HierarchicalLayout = ({ agents, currentLayout, onLayoutChange }: HierarchicalLayoutProps) => {
    return (
        <div className="relative h-full w-full">
            <ReactFlowProvider>
                <AgentMeshFlowRenderer agents={agents} currentLayoutProp={LayoutType.HIERARCHICAL} />
            </ReactFlowProvider>

            {currentLayout && onLayoutChange && (
                <div className="absolute top-0 right-6 z-20">
                    <LayoutSelector currentLayout={currentLayout} onLayoutChange={onLayoutChange} />
                </div>
            )}
        </div>
    );
};
