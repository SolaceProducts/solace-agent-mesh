// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Eye, EyeOff, RefreshCcw, Trash } from "lucide-react";
import type { ReactNode } from "react";

import { useChatArtifacts } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts";

import type { MenuAction } from "@/ui/menu";
import { Menu } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

interface ArtifactMorePopoverProps {
    children: ReactNode;
    hideDeleteAll?: boolean;
    showWorkingArtifacts?: boolean;
    onToggleWorkingArtifacts?: () => void;
    workingArtifactCount?: number;
}

export function ArtifactMorePopover({ children, hideDeleteAll = false, showWorkingArtifacts = false, onToggleWorkingArtifacts, workingArtifactCount = 0 }: ArtifactMorePopoverProps) {
    const { setIsBatchDeleteModalOpen } = useChatContext();
    const { refetch: artifactsRefetch } = useChatArtifacts();

    const menuActions: MenuAction[] = [];

    if (onToggleWorkingArtifacts) {
        const countLabel = workingArtifactCount > 0 ? ` (${workingArtifactCount})` : "";
        menuActions.push({
            id: "toggleWorking",
            label: showWorkingArtifacts ? `Hide Working Files${countLabel}` : `Show Working Files${countLabel}`,
            onClick: onToggleWorkingArtifacts,
            icon: showWorkingArtifacts ? <EyeOff /> : <Eye />,
            iconPosition: "left",
        });
    }

    menuActions.push({
        id: "refreshAll",
        label: "Refresh",
        onClick: () => {
            artifactsRefetch();
        },
        icon: <RefreshCcw />,
        iconPosition: "left",
        divider: !!onToggleWorkingArtifacts,
    });

    if (!hideDeleteAll) {
        menuActions.push({
            id: "deleteAll",
            label: "Delete All",
            onClick: () => {
                setIsBatchDeleteModalOpen(true);
            },
            icon: <Trash />,
            iconPosition: "left",
            divider: true,
        });
    }

    return (
        <Popover>
            <PopoverTrigger asChild>{children}</PopoverTrigger>
            <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0}>
                <Menu actions={menuActions} />
            </PopoverContent>
        </Popover>
    );
}
