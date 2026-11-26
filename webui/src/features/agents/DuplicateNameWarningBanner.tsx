// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { MessageBanner } from "@/common/banners/MessageBanner";

import { Button } from "@/ui/button";

import { useCombinedDuplicateBannerDismissal, useDuplicateBannerDismissal } from "@/features/agents/hooks";

interface DuplicateNameWarningBannerProps {
    agentName?: string;
    count?: number;
    duplicateAgentIds?: string[];
    duplicateGroups?: Array<{ name: string; count: number; duplicateAgentIds: string[] }>;
    dismissible?: boolean;
    showEditButton?: boolean;
    onEdit?: () => void;
    onDismiss?: () => void;
    className?: string;
}

export function DuplicateNameWarningBanner({ agentName, count, duplicateAgentIds = [], duplicateGroups, dismissible = false, showEditButton = false, onEdit, onDismiss, className = "" }: DuplicateNameWarningBannerProps) {
    const isMultipleMode = !!duplicateGroups && duplicateGroups.length > 0;

    const singleDismissal = useDuplicateBannerDismissal(agentName, duplicateAgentIds);
    const combinedDismissal = useCombinedDuplicateBannerDismissal(duplicateGroups || []);

    const { isDismissed, dismiss } = isMultipleMode ? combinedDismissal : singleDismissal;

    const handleDismiss = () => {
        dismiss();
        onDismiss?.();
    };

    if (isDismissed) return null;

    const message = (
        <div className="flex w-full items-center justify-between">
            {isMultipleMode ? (
                <span>
                    <span className="font-medium">Multiple agents share a name. Rename agents so that each name is unique: </span>
                    <span className="font-semibold">{duplicateGroups.map(group => group.name).join(", ")}</span>
                    <span>.</span>
                </span>
            ) : (
                <span>
                    <span className="font-medium">{count} agents have the same name: </span>
                    <span className="font-semibold">{agentName}</span>
                    <span>. Unique names make it easier for others in your organization to find the correct agent.</span>
                </span>
            )}

            {showEditButton && onEdit && (
                <Button variant="outline" size="sm" onClick={onEdit} className="ml-4 shrink-0">
                    Edit Agent
                </Button>
            )}
        </div>
    );

    return <MessageBanner variant="warning" message={message} dismissible={dismissible} onDismiss={dismissible ? handleDismiss : undefined} className={className} data-testid="duplicate-name-warning-banner" />;
}
