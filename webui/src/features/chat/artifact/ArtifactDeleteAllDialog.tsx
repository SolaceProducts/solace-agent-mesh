// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect } from "react";

import { useChatContext } from "@/providers/contexts";

import { hasWorkingTag } from "@/features/chat/artifactTags";

import { ConfirmationDialog } from "@/common";
import type { ArtifactInfo } from "@/types";

const files = (count: number) => (count === 1 ? "file" : "files");
const theirProjects = (count: number) => (count === 1 ? "the project" : "their projects");

function deletionSummary(total: number, projectCount: number): string {
    const regularCount = total - projectCount;
    if (projectCount > 0 && regularCount === 0) {
        const subject = total === 1 ? "This file" : "All " + total + " files";
        const again = total === 1 ? "The file" : "These files";
        return `${subject} will be removed from this chat session. ${again} will remain in ${theirProjects(total)}.`;
    }
    if (projectCount > 0) {
        return `${regularCount} ${files(regularCount)} will be permanently deleted. ${projectCount} project ${files(projectCount)} will be removed from this chat but will remain in ${theirProjects(projectCount)}.`;
    }
    const subject = total === 1 ? "One file" : "All " + total + " files";
    return `${subject} will be permanently deleted.`;
}

function workingNote(count: number): string {
    return count === 0 ? "" : ` This includes ${count} working ${files(count)} the agent may still need.`;
}

interface ArtifactDeleteAllDialogProps {
    artifacts: ArtifactInfo[];
}

export const ArtifactDeleteAllDialog: React.FC<Readonly<ArtifactDeleteAllDialogProps>> = ({ artifacts }) => {
    const { isBatchDeleteModalOpen, setIsBatchDeleteModalOpen, confirmBatchDeleteArtifacts, setSelectedArtifactFilenames } = useChatContext();

    useEffect(() => {
        if (!isBatchDeleteModalOpen) {
            return;
        }

        setSelectedArtifactFilenames(new Set(artifacts.map(artifact => artifact.filename)));
    }, [artifacts, isBatchDeleteModalOpen, setSelectedArtifactFilenames]);

    if (!isBatchDeleteModalOpen) {
        return null;
    }

    const projectArtifactsCount = artifacts.filter(artifact => artifact.source === "project").length;
    const workingArtifactsCount = artifacts.filter(artifact => artifact.source !== "project" && hasWorkingTag(artifact.tags)).length;
    const description = deletionSummary(artifacts.length, projectArtifactsCount) + workingNote(workingArtifactsCount);

    return (
        <ConfirmationDialog
            title="Delete All?"
            description={description}
            actionLabels={{ confirm: "Delete" }}
            onCancel={() => setIsBatchDeleteModalOpen(false)}
            onConfirm={confirmBatchDeleteArtifacts}
            open={isBatchDeleteModalOpen}
            onOpenChange={setIsBatchDeleteModalOpen}
        />
    );
};
