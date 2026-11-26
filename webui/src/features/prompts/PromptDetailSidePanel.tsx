// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Calendar, ChevronDown, Download, History, MoreHorizontal, NotepadText, Pencil, SquarePen, Tag, Trash2, User, X } from "lucide-react";

import { useDisplayName } from "@/api/people";

import { formatRelativeTime } from "@/utils/format";
import { formatPromptDate } from "@/utils/promptUtils";

import type { PromptGroup } from "@/types/prompts";

import { useIsPromptSharingEnabled, useIsPromptVersionHistoryEnabled } from "@/hooks";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger, RichTooltip, RichTooltipContent, RichTooltipTrigger, Tooltip, TooltipContent, TooltipTrigger } from "@/ui";

import { usePromptAccess } from "./usePromptAccess";

interface PromptDetailSidePanelProps {
    prompt: PromptGroup | null;
    onClose: () => void;
    onEdit: (prompt: PromptGroup) => void;
    onDelete: (id: string, name: string) => void;
    onViewVersions?: (prompt: PromptGroup) => void;
    onUseInChat?: (prompt: PromptGroup) => void;
    onTogglePin?: (id: string, currentStatus: boolean) => void;
    onExport?: (prompt: PromptGroup) => void;
}

function PromptAuditFooter({ prompt }: Readonly<{ prompt: PromptGroup }>) {
    const lastUpdate = prompt.productionPrompt?.createdAt ?? prompt.updatedAt ?? prompt.createdAt;
    const lastUpdaterEmail = prompt.productionPrompt?.userId ?? prompt.userId;
    const createdByName = useDisplayName(prompt.userId);
    const updatedByName = useDisplayName(lastUpdaterEmail);
    return (
        <RichTooltip>
            <div className="flex items-center gap-2 text-xs text-(--secondary-text-wMain)">
                <span>Last Updated on {formatRelativeTime(lastUpdate)}</span>
                <RichTooltipTrigger asChild>
                    <Button variant="ghost" size="sm" aria-label="Show prompt metadata" className="h-6 w-6 p-0">
                        <ChevronDown className="h-3.5 w-3.5" />
                    </Button>
                </RichTooltipTrigger>
            </div>
            <RichTooltipContent side="top" align="start" className="bg-(--background-w10) text-(--secondary-text-wMain)">
                <div className="grid grid-cols-[auto_1fr] gap-x-3 text-xs">
                    <span>Last Updated:</span>
                    <span>
                        {formatRelativeTime(lastUpdate)} by <span className="font-medium">{updatedByName}</span>
                    </span>
                    <span>Created:</span>
                    <span>
                        {formatRelativeTime(prompt.createdAt)} by <span className="font-medium">{createdByName}</span>
                    </span>
                </div>
            </RichTooltipContent>
        </RichTooltip>
    );
}

export const PromptDetailSidePanel: React.FC<PromptDetailSidePanelProps> = ({ prompt, onClose, onEdit, onDelete, onViewVersions, onUseInChat, onExport }) => {
    const versionHistoryEnabled = useIsPromptVersionHistoryEnabled();
    const sharingEnabled = useIsPromptSharingEnabled();
    const { isViewer, isOwner } = usePromptAccess(prompt);
    if (!prompt) return null;
    const showEdit = !isViewer;
    const showVersionHistory = versionHistoryEnabled && onViewVersions && !isViewer;
    const showDelete = isOwner;

    const handleEdit = () => {
        onEdit(prompt);
    };

    const handleDelete = () => {
        onDelete(prompt.id, prompt.name);
    };

    const handleViewVersions = () => {
        if (onViewVersions) {
            onViewVersions(prompt);
        }
    };

    const handleUseInChat = () => {
        if (onUseInChat) {
            onUseInChat(prompt);
        }
    };

    const handleExport = () => {
        if (onExport) {
            onExport(prompt);
        }
    };

    return (
        <div className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="border-b p-4">
                <div className="mb-2 flex items-center justify-between">
                    <div className="flex min-w-0 flex-1 items-center gap-2">
                        <NotepadText className="h-6 w-6 flex-shrink-0 text-(--brand-wMain)" />
                        <Tooltip delayDuration={300}>
                            <TooltipTrigger asChild>
                                <h2 className="cursor-default truncate text-lg font-semibold">{prompt.name}</h2>
                            </TooltipTrigger>
                            <TooltipContent side="bottom">
                                <p>{prompt.name}</p>
                            </TooltipContent>
                        </Tooltip>
                    </div>
                    <div className="ml-2 flex flex-shrink-0 items-center gap-1">
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                                <Button variant="ghost" size="sm" className="h-8 w-8 p-0" tooltip="Actions">
                                    <MoreHorizontal className="h-4 w-4" />
                                </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
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
                        <Button variant="ghost" size="sm" onClick={onClose} className="h-8 w-8 p-0" tooltip="Close">
                            <X className="h-4 w-4" />
                        </Button>
                    </div>
                </div>
                {(prompt.productionPrompt?.category || prompt.category) && (
                    <span className="inline-flex items-center gap-1 rounded-full bg-(--primary-w10) px-2.5 py-0.5 text-xs font-medium text-(--primary-wMain)">
                        <Tag size={12} />
                        {prompt.productionPrompt?.category || prompt.category}
                    </span>
                )}
            </div>

            <div className="flex-1 space-y-6 overflow-y-auto p-4">
                <div className="space-y-6 rounded bg-(--secondary-w10) p-4">
                    <div>
                        <h3 className="mb-2 text-xs font-semibold text-(--secondary-text-wMain)">Description</h3>
                        <div className="text-sm leading-relaxed">{prompt.productionPrompt?.description || prompt.description || "No description provided."}</div>
                    </div>

                    {(prompt.productionPrompt?.command || prompt.command) && (
                        <div>
                            <h3 className="mb-2 text-xs font-semibold text-(--secondary-text-wMain)">Chat Shortcut</h3>
                            <div>
                                <span className="inline-block rounded bg-(--primary-w10) px-2 py-0.5 font-mono text-xs text-(--info-wMain)">/{prompt.productionPrompt?.command || prompt.command}</span>
                            </div>
                        </div>
                    )}
                </div>

                {onUseInChat && (
                    <Button data-testid="startNewChatButton" onClick={handleUseInChat} className="w-full">
                        <SquarePen className="h-4 w-4" />
                        Use in New Chat
                    </Button>
                )}

                {prompt.productionPrompt && (
                    <div>
                        <h3 className="mb-2 text-xs font-semibold text-(--secondary-text-wMain)">Content</h3>
                        <div className="font-mono text-xs break-words whitespace-pre-wrap">
                            {prompt.productionPrompt.promptText.split(/(\{\{[^}]+\}\})/g).map((part, index) => {
                                if (part.match(/\{\{[^}]+\}\}/)) {
                                    return (
                                        <span key={index} className="rounded bg-(--primary-w20) px-1 font-medium text-(--primary-wMain)">
                                            {part}
                                        </span>
                                    );
                                }
                                return <span key={index}>{part}</span>;
                            })}
                        </div>
                    </div>
                )}
            </div>

            {sharingEnabled ? (
                <div className="border-t bg-(--background-w10) p-4">
                    <PromptAuditFooter prompt={prompt} />
                </div>
            ) : (
                <div className="space-y-2 border-t bg-(--background-w10) p-4">
                    <div className="flex items-center gap-2 text-xs text-(--secondary-text-wMain)">
                        <User size={12} />
                        <span>Created by: {prompt.authorName || prompt.userId}</span>
                    </div>
                    {prompt.updatedAt && (
                        <div className="flex items-center gap-2 text-xs text-(--secondary-text-wMain)">
                            <Calendar size={12} />
                            <span>Last updated: {formatPromptDate(prompt.updatedAt)}</span>
                        </div>
                    )}
                </div>
            )}
        </div>
    );
};
