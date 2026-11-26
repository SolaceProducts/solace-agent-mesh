// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Project } from "@/types/projects";

export interface OwnershipContext {
    username?: string;
    configUseAuthorization: boolean;
    isSharingEnabled: boolean;
}

export const isProjectOwnedByCurrentUser = (projectUserId: string, { username, configUseAuthorization, isSharingEnabled }: OwnershipContext): boolean => {
    if (!configUseAuthorization || !isSharingEnabled) {
        return true;
    }
    const normalizedUsername = username?.toLowerCase();
    return normalizedUsername !== undefined && normalizedUsername.length > 0 && projectUserId.toLowerCase() === normalizedUsername;
};

export const selectOwnedProjectNames = (projects: Project[], context: OwnershipContext): string[] => projects.filter(project => isProjectOwnedByCurrentUser(project.userId, context)).map(project => project.name);
