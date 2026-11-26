// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Eye, Pencil } from "lucide-react";

import { useCreatePromptShares, usePromptShares, useReplacePromptShares } from "@/api/prompt-sharing";

import { useChatContext, useConfigContext } from "@/providers/contexts";

import { type AccessLevelOption, ShareResourceDialog, type ShareSaveSummary } from "@/common/dialogs/ShareResourceDialog";

import type { PromptGroup } from "@/types/prompts";
import type { PromptShareAccessLevel } from "@/types/promptSharing";

interface SharePromptDialogProps {
    isOpen: boolean;
    onClose: () => void;
    promptGroup: PromptGroup;
}

const PROMPT_ACCESS_LEVELS: AccessLevelOption<PromptShareAccessLevel>[] = [
    { value: "resource_viewer", label: "Viewer", icon: <Eye className="h-4 w-4" />, description: "Can use the most active version" },
    { value: "resource_editor", label: "Editor", icon: <Pencil className="h-4 w-4" />, description: "Can update and create new versions" },
];

export function SharePromptDialog({ isOpen, onClose, promptGroup }: Readonly<SharePromptDialogProps>) {
    const { identityServiceType } = useConfigContext();
    const { addNotification } = useChatContext();
    const { data, isLoading } = usePromptShares(promptGroup.id);
    const createShares = useCreatePromptShares();
    const replaceShares = useReplacePromptShares();

    const shares = (data?.data ?? []).map(s => ({
        id: s.id,
        userEmail: s.userEmail,
        accessLevel: s.accessLevel,
    }));

    const handleSuccess = ({ added, removed }: ShareSaveSummary) => {
        if (added.length > 0) {
            addNotification(`${promptGroup.name} shared with ${added.length} ${added.length === 1 ? "user" : "users"}`, "success");
        }
        if (removed.length === 1) {
            addNotification(`${promptGroup.name} removed for ${removed[0]}`, "success");
        } else if (removed.length > 1) {
            addNotification(`${promptGroup.name} removed for ${removed.length} users`, "success");
        }
    };

    return (
        <ShareResourceDialog<PromptShareAccessLevel>
            isOpen={isOpen}
            onClose={onClose}
            resourceLabel="Prompt"
            resourceName={promptGroup.name}
            ownerEmail={promptGroup.userId}
            shares={shares}
            isLoading={isLoading}
            identityServiceType={identityServiceType}
            accessLevels={PROMPT_ACCESS_LEVELS}
            onCreate={async entries => {
                await createShares.mutateAsync({ promptGroupId: promptGroup.id, body: { shares: entries } });
            }}
            onReplace={async entries => {
                await replaceShares.mutateAsync({ promptGroupId: promptGroup.id, body: { shares: entries } });
            }}
            onSuccess={handleSuccess}
        />
    );
}
