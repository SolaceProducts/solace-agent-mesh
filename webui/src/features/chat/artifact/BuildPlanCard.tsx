// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useState } from "react";

import { useBuilderContext, useChatContext } from "@/providers/contexts";

import { ApprovedPlanTimeline } from "./ApprovedPlanTimeline";
import { BuildPlanEditForm } from "./BuildPlanEditForm";
import { StreamingPlanCard } from "./StreamingPlanCard";
import { SuggestedPlanView } from "./SuggestedPlanView";
import { type BuildManifest, useBuildPlanArtifact } from "./useBuildPlanArtifact";

export { parseManifest, trimToCompleteYaml } from "./useBuildPlanArtifact";

const COMPONENT_TYPE_ORDER = ["gateway", "workflow", "agent", "skill", "connector"] as const;

export interface BuildPlanCardProps {
    filename: string;
    isComplete: boolean;
    onBuildActivate?: (manifest: BuildManifest) => void;
    onKeepEditing?: () => void;
    onPlanEdited?: (editSummary: string) => void;
    hideActions?: boolean;
    isMessageComplete?: boolean;
    onTestRequest?: (targetName: string, targetType: "agent" | "workflow", displayName: string) => void;
}

export const BuildPlanCard: React.FC<BuildPlanCardProps> = ({ filename, isComplete, onBuildActivate, onKeepEditing, onPlanEdited, hideActions = false, isMessageComplete = true, onTestRequest }) => {
    const { handleSubmit, isResponding } = useChatContext();
    const { builderMode } = useBuilderContext();

    const { manifest } = useBuildPlanArtifact({ filename, isComplete });

    const [isEditing, setIsEditing] = useState(false);
    const [editedDraft, setEditedDraft] = useState<BuildManifest | null>(null);
    const [actioned, setActioned] = useState(false);

    const handleEditPlan = useCallback(() => {
        if (manifest) {
            setEditedDraft(JSON.parse(JSON.stringify(manifest)));
            setIsEditing(true);
        }
    }, [manifest]);

    const handleCancelEdit = useCallback(() => {
        setIsEditing(false);
        setEditedDraft(null);
    }, []);

    const handleSubmitEdit = useCallback(
        (summary: string) => {
            setIsEditing(false);
            setEditedDraft(null);
            if (onPlanEdited) {
                onPlanEdited(summary);
            } else {
                const fakeEvent = { preventDefault: () => {} } as React.FormEvent;
                handleSubmit(fakeEvent, null, summary);
            }
        },
        [onPlanEdited, handleSubmit]
    );

    const handleBuildActivate = useCallback(() => {
        setActioned(true);
        if (onBuildActivate && manifest) {
            onBuildActivate(manifest);
        } else {
            const fakeEvent = { preventDefault: () => {} } as React.FormEvent;
            handleSubmit(fakeEvent, null, "Build it.", null, null, null, null, null, {
                chip: { emoji: "✅", label: "Plan approved" },
            });
        }
    }, [onBuildActivate, manifest, handleSubmit]);

    const handleKeepEditing = useCallback(() => {
        setActioned(true);
        if (onKeepEditing) {
            onKeepEditing();
        } else {
            const fakeEvent = { preventDefault: () => {} } as React.FormEvent;
            handleSubmit(fakeEvent, null, "I'd like to make some changes to this plan.", null, null, null, null, null, {
                chip: { emoji: "✏️", label: "Editing plan" },
            });
        }
    }, [onKeepEditing, handleSubmit]);

    if (builderMode) {
        return null;
    }

    if (!isComplete) {
        return <StreamingPlanCard manifest={manifest} />;
    }

    if (isEditing && editedDraft && manifest) {
        return <BuildPlanEditForm originalManifest={manifest} initialEditedManifest={editedDraft} onSubmit={handleSubmitEdit} onCancel={handleCancelEdit} />;
    }

    const isActivelyBuilding = isComplete && !isEditing && (!isMessageComplete || (builderMode && isResponding && actioned));

    const displayComponents = manifest?.components || [];
    const sortedComponents = [...displayComponents].sort((a, b) => {
        const aIdx = COMPONENT_TYPE_ORDER.indexOf((a.type || "agent") as (typeof COMPONENT_TYPE_ORDER)[number]);
        const bIdx = COMPONENT_TYPE_ORDER.indexOf((b.type || "agent") as (typeof COMPONENT_TYPE_ORDER)[number]);
        return (aIdx === -1 ? 99 : aIdx) - (bIdx === -1 ? 99 : bIdx);
    });

    if (actioned) {
        return <ApprovedPlanTimeline manifest={manifest} sortedComponents={sortedComponents} isActivelyBuilding={isActivelyBuilding} onTestRequest={onTestRequest} isResponding={isResponding} />;
    }

    return <SuggestedPlanView manifest={manifest} sortedComponents={sortedComponents} hideActions={hideActions} onEdit={handleEditPlan} onKeepEditing={handleKeepEditing} onBuildActivate={handleBuildActivate} />;
};
