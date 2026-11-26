// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo } from "react";
import { Eye } from "lucide-react";

import { useCreateProjectShares, useDeleteProjectShares, useProjectShares } from "@/api/projects/hooks";

import { useChatContext, useConfigContext } from "@/providers/contexts";

import { type AccessLevelOption, ShareResourceDialog, type ShareResourceEntry, type ShareSaveSummary } from "@/common/dialogs/ShareResourceDialog";

import type { Project } from "@/types/projects";

type ProjectAccessLevel = "resource_viewer";

const PROJECT_ACCESS_LEVELS: AccessLevelOption<ProjectAccessLevel>[] = [{ value: "resource_viewer", label: "Viewer", icon: <Eye className="h-4 w-4" />, description: "Can view the items in the project" }];

interface ShareProjectDialogProps {
    isOpen: boolean;
    onClose: () => void;
    project: Project;
}

export function ShareProjectDialog({ isOpen, onClose, project }: Readonly<ShareProjectDialogProps>) {
    const { identityServiceType } = useConfigContext();
    const { addNotification } = useChatContext();

    const { data: sharesData, isLoading } = useProjectShares(project.id);
    const createShares = useCreateProjectShares();
    const deleteShares = useDeleteProjectShares();

    const sharedUsers = useMemo(() => sharesData?.shares ?? [], [sharesData?.shares]);
    const ownerEmail = sharesData?.ownerEmail;

    const shares: ShareResourceEntry<ProjectAccessLevel>[] = useMemo(() => sharedUsers.map(s => ({ id: s.id, userEmail: s.userEmail, accessLevel: "resource_viewer" })), [sharedUsers]);

    const userByEmail = useMemo(() => new Map(sharedUsers.map(s => [s.userEmail, s])), [sharedUsers]);

    const handleCreate = useCallback(
        async (entries: Array<{ userEmail: string; accessLevel: ProjectAccessLevel }>) => {
            await createShares.mutateAsync({
                projectId: project.id,
                data: { shares: entries.map(e => ({ userEmail: e.userEmail, accessLevel: e.accessLevel })) },
            });
        },
        [createShares, project.id]
    );

    const handleReplace = useCallback(
        async (desired: Array<{ userEmail: string; accessLevel: ProjectAccessLevel }>) => {
            const desiredByEmail = new Map(desired.map(e => [e.userEmail, e.accessLevel]));
            const toRemove = sharedUsers.filter(u => !desiredByEmail.has(u.userEmail)).map(u => u.userEmail);
            const toAdd = desired.filter(e => userByEmail.get(e.userEmail)?.accessLevel !== e.accessLevel);

            if (toAdd.length > 0) {
                await createShares.mutateAsync({
                    projectId: project.id,
                    data: { shares: toAdd.map(e => ({ userEmail: e.userEmail, accessLevel: e.accessLevel })) },
                });
            }
            if (toRemove.length > 0) {
                await deleteShares.mutateAsync({
                    projectId: project.id,
                    data: { userEmails: toRemove },
                });
            }
        },
        [sharedUsers, userByEmail, createShares, deleteShares, project.id]
    );

    const handleSuccess = ({ added, removed }: ShareSaveSummary) => {
        if (added.length > 0) {
            const userText = added.length === 1 ? "user" : "users";
            addNotification(`${added.length} ${userText} added to project`, "success");
        }
        if (removed.length > 0) {
            const userText = removed.length === 1 ? "user" : "users";
            addNotification(`${removed.length} ${userText} removed from project`, "success");
        }
    };

    return (
        <ShareResourceDialog<ProjectAccessLevel>
            isOpen={isOpen}
            onClose={onClose}
            resourceLabel="Project"
            resourceName={project.name}
            ownerEmail={ownerEmail}
            shares={shares}
            isLoading={isLoading}
            identityServiceType={identityServiceType}
            accessLevels={PROJECT_ACCESS_LEVELS}
            onCreate={handleCreate}
            onReplace={handleReplace}
            onSuccess={handleSuccess}
            description={
                <>
                    Invite others to collaborate on <strong>{project.name}</strong>.
                </>
            }
        />
    );
}
