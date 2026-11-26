// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import { Clock, Download, Eye, History, MessageSquare, MoreHorizontal, NotepadText, Pencil, Share2, Tag, Trash2, UserIcon } from "lucide-react";

import type { PromptGroup } from "@/types/prompts";

import { GridCard, PinButton } from "@/common";
import { useIsPromptVersionHistoryEnabled } from "@/hooks";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger, Tooltip, TooltipContent, TooltipTrigger } from "@/ui";

import { usePromptAccess } from "./usePromptAccess";

interface PromptDisplayCardProps {
    prompt: PromptGroup;
    isSelected: boolean;
    onPromptClick: () => void;
    onEdit: (prompt: PromptGroup) => void;
    onDelete: (id: string, name: string) => void;
    onViewVersions?: (prompt: PromptGroup) => void;
    onUseInChat?: (prompt: PromptGroup) => void;
    onTogglePin?: (id: string, currentStatus: boolean) => void;
    onExport?: (prompt: PromptGroup) => void;
    onShare?: (prompt: PromptGroup) => void;
}

const ACCESS_ICON_BY_LEVEL = {
    owner: { Icon: UserIcon, label: "You are the owner of this prompt" },
    resource_editor: { Icon: Pencil, label: "You are an editor of this prompt" },
    resource_viewer: { Icon: Eye, label: "You are a viewer of this prompt" },
} as const;

function AccessLevelIcon({ level }: Readonly<{ level: NonNullable<PromptGroup["accessLevel"]> }>) {
    const meta = ACCESS_ICON_BY_LEVEL[level];
    if (!meta) return null;
    const { Icon, label } = meta;
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span aria-label={label} className="cursor-default text-(--secondary-text-wMain)">
                    <Icon className="h-5 w-5" />
                </span>
            </TooltipTrigger>
            <TooltipContent side="top">{label}</TooltipContent>
        </Tooltip>
    );
}

export const PromptCard: React.FC<PromptDisplayCardProps> = ({ prompt, isSelected, onPromptClick, onEdit, onDelete, onViewVersions, onUseInChat, onTogglePin, onExport, onShare }) => {
    const versionHistoryEnabled = useIsPromptVersionHistoryEnabled();
    const { isViewer, isOwner, canShare } = usePromptAccess(prompt);
    const [dropdownOpen, setDropdownOpen] = useState(false);

    const showEdit = !isViewer;
    const showVersionHistory = versionHistoryEnabled && onViewVersions && !isViewer;
    const showShare = canShare && onShare;
    const showDelete = isOwner;
    const handleEdit = (e: React.MouseEvent) => {
        e.stopPropagation();
        setDropdownOpen(false);
        onEdit(prompt);
    };

    const handleDelete = (e: React.MouseEvent) => {
        e.stopPropagation();
        setDropdownOpen(false);
        onDelete(prompt.id, prompt.name);
    };

    const handleViewVersions = (e: React.MouseEvent) => {
        e.stopPropagation();
        setDropdownOpen(false);
        if (onViewVersions) {
            onViewVersions(prompt);
        }
    };

    const handleUseInChat = (e: React.MouseEvent) => {
        e.stopPropagation();
        setDropdownOpen(false);
        if (onUseInChat) {
            onUseInChat(prompt);
        }
    };

    const handleTogglePin = (e: React.MouseEvent) => {
        e.stopPropagation();
        if (onTogglePin) {
            onTogglePin(prompt.id, prompt.isPinned);
        }
    };

    const handleExport = (e: React.MouseEvent) => {
        e.stopPropagation();
        setDropdownOpen(false);
        if (onExport) {
            onExport(prompt);
        }
    };

    return (
        <GridCard data-testid={prompt.id} isSelected={isSelected} onClick={onPromptClick}>
            <div className="flex h-full w-full flex-col">
                <div className="flex items-center justify-between px-4">
                    <div className="flex min-w-0 flex-1 items-center gap-2">
                        <NotepadText className="h-6 w-6 flex-shrink-0 text-(--brand-wMain)" />
                        <div className="min-w-0">
                            <h2 className="truncate text-lg font-semibold" title={prompt.name}>
                                {prompt.name}
                            </h2>
                            {prompt.command && (
                                <div className="truncate font-mono text-xs text-(--secondary-text-wMain)" title={`/${prompt.command}`}>
                                    /{prompt.command}
                                </div>
                            )}
                        </div>
                    </div>
                    <div className="flex items-center gap-1">
                        {onTogglePin && <PinButton isPinned={prompt.isPinned} onClick={handleTogglePin} />}
                        <DropdownMenu open={dropdownOpen} onOpenChange={setDropdownOpen}>
                            <DropdownMenuTrigger asChild>
                                <Button variant="ghost" size="icon" onClick={e => e.stopPropagation()} tooltip="Actions" className="cursor-pointer">
                                    <MoreHorizontal size={16} />
                                </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end" onClick={e => e.stopPropagation()}>
                                {onUseInChat && (
                                    <DropdownMenuItem onClick={handleUseInChat}>
                                        <MessageSquare size={14} className="mr-2" />
                                        Use in New Chat
                                    </DropdownMenuItem>
                                )}
                                {showShare && (
                                    <DropdownMenuItem
                                        onClick={(e: React.MouseEvent) => {
                                            e.stopPropagation();
                                            setDropdownOpen(false);
                                            onShare(prompt);
                                        }}
                                    >
                                        <Share2 size={14} className="mr-2" />
                                        Share
                                    </DropdownMenuItem>
                                )}
                                {onExport && (
                                    <DropdownMenuItem onClick={handleExport}>
                                        <Download size={14} className="mr-2" />
                                        Export Prompt
                                    </DropdownMenuItem>
                                )}
                                {showEdit && (
                                    <DropdownMenuItem onClick={handleEdit}>
                                        <Pencil size={14} className="mr-2" />
                                        Edit Prompt
                                    </DropdownMenuItem>
                                )}
                                {showVersionHistory && (
                                    <DropdownMenuItem onClick={handleViewVersions}>
                                        <History size={14} className="mr-2" />
                                        Open Version History
                                    </DropdownMenuItem>
                                )}
                                {showDelete && (
                                    <>
                                        <DropdownMenuSeparator />
                                        <DropdownMenuItem onClick={handleDelete}>
                                            <Trash2 size={14} className="mr-2" />
                                            {versionHistoryEnabled ? "Delete All Versions" : "Delete"}
                                        </DropdownMenuItem>
                                    </>
                                )}
                            </DropdownMenuContent>
                        </DropdownMenu>
                    </div>
                </div>
                <div className="flex flex-grow flex-col overflow-hidden px-4 pt-2">
                    <div className="mb-2 line-clamp-2 flex-shrink-0 text-sm leading-5">{prompt.productionPrompt?.description || prompt.description || "No description provided."}</div>
                    {versionHistoryEnabled && prompt.productionPrompt?.version !== undefined && (
                        <div className="mb-1 flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                            <Clock size={12} className="flex-shrink-0" />
                            <span>
                                Active Version: <span className="font-medium text-(--primary-text-wMain)">{prompt.productionPrompt.version}</span>
                            </span>
                        </div>
                    )}
                    {prompt.category && (
                        <div className="mb-2 flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                            <Tag size={12} className="flex-shrink-0" />
                            <span className="truncate">{prompt.category}</span>
                        </div>
                    )}
                    <div className="mt-auto flex items-center justify-between gap-2">
                        <span className="truncate text-xs text-(--secondary-text-wMain)">{prompt.authorName || prompt.userId}</span>
                        {prompt.accessLevel && <AccessLevelIcon level={prompt.accessLevel} />}
                    </div>
                </div>
            </div>
        </GridCard>
    );
};
