// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { PromptGroup } from "@/types/prompts";

import { useIsPromptSharingEnabled } from "@/hooks";

export interface PromptAccess {
    sharingEnabled: boolean;
    isViewer: boolean;
    isEditor: boolean;
    isOwner: boolean;
    canShare: boolean;
}

export function usePromptAccess(prompt: Pick<PromptGroup, "accessLevel"> | null | undefined): PromptAccess {
    const sharingEnabled = useIsPromptSharingEnabled();
    const accessLevel = prompt?.accessLevel;
    const isOwner = !sharingEnabled || accessLevel === "owner";
    return {
        sharingEnabled,
        isViewer: sharingEnabled && accessLevel === "resource_viewer",
        isEditor: sharingEnabled && accessLevel === "resource_editor",
        isOwner,
        canShare: sharingEnabled && isOwner,
    };
}
