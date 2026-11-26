// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { FolderInput, MoreHorizontal, PanelsTopLeft, Pencil, Share2, Sparkles, Trash2 } from "lucide-react";

import { useUIModeContext } from "@/providers/contexts";

import type { Session } from "@/types";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/ui";
import { cn } from "@/utils";

export interface SessionActionMenuProps {
    session: Session;
    onRename: (session: Session) => void;
    onRenameWithAI: (session: Session) => void;
    onMove: (session: Session) => void;
    onDelete: (session: Session) => void;
    onGoToProject?: (session: Session) => void;
    onShare?: (session: Session) => void;
    isRegeneratingTitle?: boolean;
    triggerClassName?: string;
}

export const SessionActionMenu: React.FC<SessionActionMenuProps> = ({ session, onRename, onRenameWithAI, onMove, onDelete, onGoToProject, onShare, isRegeneratingTitle = false, triggerClassName }) => {
    const { allowedSessionActions } = useUIModeContext();
    const showGoToProject = allowedSessionActions.includes("goToProject") && !!session.project?.id && !!onGoToProject;
    const showShare = allowedSessionActions.includes("share") && !!onShare;

    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <Button
                    variant="ghost"
                    size="sm"
                    className={cn("h-8 w-8 p-0", triggerClassName)}
                    onClick={e => e.stopPropagation()}
                    onKeyDown={e => {
                        if (e.key === "Enter" || e.key === " ") {
                            e.stopPropagation();
                        }
                    }}
                >
                    <MoreHorizontal size={16} />
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
                {showGoToProject && (
                    <>
                        <DropdownMenuItem
                            onClick={e => {
                                e.stopPropagation();
                                onGoToProject?.(session);
                            }}
                        >
                            <PanelsTopLeft size={16} className="mr-2" />
                            Go to Project
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                    </>
                )}
                {allowedSessionActions.includes("rename") && (
                    <DropdownMenuItem
                        onClick={e => {
                            e.stopPropagation();
                            onRename(session);
                        }}
                    >
                        <Pencil size={16} className="mr-2" />
                        Rename
                    </DropdownMenuItem>
                )}
                {allowedSessionActions.includes("renameWithAI") && (
                    <DropdownMenuItem
                        onClick={e => {
                            e.stopPropagation();
                            onRenameWithAI(session);
                        }}
                        disabled={isRegeneratingTitle}
                    >
                        <Sparkles size={16} className={cn("mr-2", isRegeneratingTitle && "animate-pulse")} />
                        Rename with AI
                    </DropdownMenuItem>
                )}
                {allowedSessionActions.includes("move") && (
                    <DropdownMenuItem
                        onClick={e => {
                            e.stopPropagation();
                            onMove(session);
                        }}
                    >
                        <FolderInput size={16} className="mr-2" />
                        Move to Project
                    </DropdownMenuItem>
                )}
                {showShare && (
                    <DropdownMenuItem
                        onClick={e => {
                            e.stopPropagation();
                            onShare?.(session);
                        }}
                    >
                        <Share2 size={16} className="mr-2" />
                        Share
                    </DropdownMenuItem>
                )}
                {allowedSessionActions.includes("delete") && (
                    <>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                            onClick={e => {
                                e.stopPropagation();
                                onDelete(session);
                            }}
                        >
                            <Trash2 size={16} className="mr-2" />
                            Delete
                        </DropdownMenuItem>
                    </>
                )}
            </DropdownMenuContent>
        </DropdownMenu>
    );
};
