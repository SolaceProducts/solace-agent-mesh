// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef } from "react";

import { useTaskContext } from "@/providers/contexts";

import { type VisualizerStep } from "@/types/activities";
import { type ArtifactInfo } from "@/types/chat";

import { VisualizerStepCard } from "@/features/activities/VisualizerStepCard";

interface LinearStepViewPanelProps {
    steps: VisualizerStep[];
    artifactLookup?: (filename: string) => ArtifactInfo | undefined;
    onViewArtifact?: (artifact: ArtifactInfo, version?: number) => void;
}

const LinearStepViewPanel: React.FC<LinearStepViewPanelProps> = ({ steps, artifactLookup, onViewArtifact }) => {
    const { highlightedStepId, setHighlightedStepId } = useTaskContext();
    const stepRefs = useRef<Record<string, HTMLDivElement | null>>({});

    useEffect(() => {
        if (highlightedStepId && stepRefs.current[highlightedStepId]) {
            stepRefs.current[highlightedStepId]?.scrollIntoView({
                behavior: "smooth",
                block: "nearest",
            });
        }
    }, [highlightedStepId]);

    if (!steps || steps.length === 0) {
        return (
            <div className="flex h-full flex-col items-center justify-center text-(--secondary-text-wMain)">
                <p className="text-md">No events to display.</p>
            </div>
        );
    }

    return (
        <div className="space-y-2">
            <div className="mb-6">
                <h2 className="text-lg font-semibold">Events</h2>
                <p className="mt-1 text-sm text-(--secondary-text-wMain)">Click an edge in the graph or an event below to highlight</p>
            </div>
            {steps.map(step => (
                <div
                    key={step.id}
                    ref={el => {
                        stepRefs.current[step.id] = el;
                    }}
                >
                    <VisualizerStepCard step={step} isHighlighted={highlightedStepId === step.id} onClick={() => setHighlightedStepId(step.id)} artifactLookup={artifactLookup} onViewArtifact={onViewArtifact} />
                </div>
            ))}
        </div>
    );
};

export default LinearStepViewPanel;
