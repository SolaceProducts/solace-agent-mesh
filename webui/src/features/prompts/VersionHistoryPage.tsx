// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useRef, useState } from "react";
import { Check, List, MoreHorizontal, Pencil, Trash2 } from "lucide-react";

import { api } from "@/api/client";
import { useDisplayName } from "@/api/people";

import { useChatContext } from "@/providers/contexts";

import { getErrorMessage } from "@/utils/api";
import { formatPromptDate } from "@/utils/promptUtils";

import type { Prompt, PromptGroup } from "@/types/prompts";

import { Header, MessageBanner } from "@/common";
import { useIsMobile, useIsPromptSharingEnabled } from "@/hooks";
import { Badge, Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger, Label, LifecycleBadge, Spinner } from "@/ui";
import { cn } from "@/utils";

import { usePromptAccess } from "./usePromptAccess";

interface VersionHistoryPageProps {
    group: PromptGroup;
    onBack: () => void;
    onBackToPromptDetail: () => void;
    onEdit: (group: PromptGroup) => void;
    onDeleteAll: (id: string, name: string) => void;
    onRestoreVersion: (promptId: string, groupId: string) => Promise<void>;
}

function PromptCreatorLine({ group }: Readonly<{ group: PromptGroup }>) {
    const ownerName = useDisplayName(group.userId);
    return (
        <div className="text-xs text-(--secondary-text-wMain)">
            Prompt Created on <span className="font-medium">{formatPromptDate(group.createdAt)}</span> by <span className="font-medium">{ownerName}</span>
        </div>
    );
}

function VersionAuthorLine({ version }: Readonly<{ version: Prompt }>) {
    const authorName = useDisplayName(version.userId);
    return (
        <div className="text-xs text-(--secondary-text-wMain)">
            Version {version.version} Created on <span className="font-medium">{formatPromptDate(version.createdAt)}</span> by <span className="font-medium">{authorName}</span>
        </div>
    );
}

interface VersionDetailsPanelProps {
    version: Prompt;
    group: PromptGroup;
    isOwner: boolean;
    isViewer: boolean;
    isActiveVersion: boolean;
    sharingEnabled: boolean;
    onEdit: () => void;
    onRestore: () => void;
    onDelete: () => void;
}

function VersionDetailsPanel({ version, group, isOwner, isViewer, isActiveVersion, sharingEnabled, onEdit, onRestore, onDelete }: Readonly<VersionDetailsPanelProps>) {
    const showEdit = !isViewer;
    const showActionsMenu = !isViewer && (!isActiveVersion || isOwner);
    return (
        <div className="p-6">
            <div className="mx-auto max-w-4xl space-y-6">
                <div className="flex items-center justify-between">
                    <h2 className="text-lg font-semibold">Version {version.version} Details</h2>
                    <div className="flex items-center gap-2">
                        {showEdit && (
                            <Button variant="ghost" size="sm" onClick={onEdit} disabled={!isActiveVersion} tooltip={isActiveVersion ? undefined : "Only the latest version can be edited. Restore this version to edit it."}>
                                <Pencil className="h-4 w-4" />
                                Edit
                            </Button>
                        )}
                        {showActionsMenu && (
                            <DropdownMenu>
                                <DropdownMenuTrigger asChild>
                                    <Button variant="ghost" size="sm">
                                        <MoreHorizontal className="h-4 w-4" />
                                    </Button>
                                </DropdownMenuTrigger>
                                <DropdownMenuContent align="end">
                                    {!isActiveVersion && (
                                        <DropdownMenuItem onClick={onRestore}>
                                            <Check size={14} className="mr-2" />
                                            Restore as New Version
                                        </DropdownMenuItem>
                                    )}
                                    {isOwner && (
                                        <>
                                            {!isActiveVersion && <DropdownMenuSeparator />}
                                            <DropdownMenuItem onClick={onDelete}>
                                                <Trash2 size={14} className="mr-2" />
                                                Delete Version
                                            </DropdownMenuItem>
                                        </>
                                    )}
                                </DropdownMenuContent>
                            </DropdownMenu>
                        )}
                    </div>
                </div>

                <div className="space-y-6">
                    <div className="space-y-2">
                        <Label className="text-(--secondary-text-wMain)">Name</Label>
                        <div className="rounded p-3 text-sm break-words whitespace-pre-wrap">{version.name || group.name}</div>
                    </div>

                    {(version.description || group.description) && (
                        <div className="space-y-2">
                            <Label className="text-(--secondary-text-wMain)">Description</Label>
                            <div className="rounded p-3 text-sm break-words whitespace-pre-wrap">{version.description || group.description}</div>
                        </div>
                    )}

                    {(version.command || group.command) && (
                        <div className="space-y-2">
                            <Label className="text-(--secondary-text-wMain)">Chat Shortcut</Label>
                            <div className="rounded p-3 text-sm">
                                <span className="font-mono text-(--primary-wMain)">/{version.command || group.command}</span>
                            </div>
                        </div>
                    )}

                    {(version.category || group.category) && (
                        <div className="space-y-2">
                            <Label className="text-(--secondary-text-wMain)">Tag</Label>
                            <div className="rounded p-3 text-sm">{version.category || group.category}</div>
                        </div>
                    )}

                    <div className="space-y-2">
                        <Label className="text-(--secondary-text-wMain)">Content</Label>
                        <div className="rounded p-3 font-mono text-sm break-words whitespace-pre-wrap">
                            {version.promptText.split(/(\{\{[^}]{1,200}\}\})/g).map((part, index) => {
                                const isVariable = index % 2 === 1;
                                const key = `${index}-${part}`;
                                if (isVariable) {
                                    return (
                                        <span key={key} className="rounded bg-(--info-w20) px-1 font-medium text-(--info-wMain)">
                                            {part}
                                        </span>
                                    );
                                }
                                return <span key={key}>{part}</span>;
                            })}
                        </div>
                    </div>

                    {sharingEnabled ? (
                        <div className="space-y-1 border-t pt-4">
                            <VersionAuthorLine version={version} />
                            <PromptCreatorLine group={group} />
                        </div>
                    ) : (
                        <div className="border-t pt-4">
                            <div className="text-xs text-(--secondary-text-wMain)">Created: {formatPromptDate(version.createdAt)}</div>
                        </div>
                    )}
                </div>
            </div>
        </div>
    );
}

export const VersionHistoryPage: React.FC<VersionHistoryPageProps> = ({ group, onBack, onEdit, onDeleteAll, onRestoreVersion }) => {
    const { addNotification, displayError } = useChatContext();
    const [versions, setVersions] = useState<Prompt[]>([]);
    const [selectedVersion, setSelectedVersion] = useState<Prompt | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const [currentGroup, setCurrentGroup] = useState<PromptGroup>(group);
    const [showDeleteActiveError, setShowDeleteActiveError] = useState(false);
    const hasInitializedRef = useRef(false);
    const isMobile = useIsMobile();
    const [mobileShowList, setMobileShowList] = useState(false);

    useEffect(() => {
        if (!isMobile) setMobileShowList(false);
    }, [isMobile]);

    const { isOwner, isViewer } = usePromptAccess(currentGroup);
    const sharingEnabled = useIsPromptSharingEnabled();

    useEffect(() => {
        setCurrentGroup(group);
    }, [group]);

    const fetchVersions = useCallback(
        async (preserveSelection = false) => {
            setIsLoading(true);
            try {
                const response = await api.webui.get(`/api/v1/prompts/groups/${group.id}/prompts`);
                const data: Prompt[] = response.data ?? [];
                setVersions(data);

                setSelectedVersion(currentSelected => {
                    if (group._selectedVersionId) {
                        const targetVersion = data.find((v: Prompt) => v.id === group._selectedVersionId);
                        if (targetVersion) {
                            return targetVersion;
                        }
                    }

                    if (preserveSelection && currentSelected) {
                        const stillExists = data.find((v: Prompt) => v.id === currentSelected.id);
                        if (stillExists) {
                            return stillExists;
                        } else {
                            return data.find((v: Prompt) => v.id === group.productionPromptId) || data[0];
                        }
                    } else if (data.length > 0 && !hasInitializedRef.current) {
                        hasInitializedRef.current = true;
                        return data.find((v: Prompt) => v.id === group.productionPromptId) || data[0];
                    }
                    return currentSelected;
                });
            } catch (error) {
                console.error("Failed to fetch versions:", error);
            } finally {
                setIsLoading(false);
            }
        },
        [group.id, group.productionPromptId, group._selectedVersionId]
    );

    const fetchGroupData = useCallback(async () => {
        try {
            const data = await api.webui.get(`/api/v1/prompts/groups/${group.id}`);
            setCurrentGroup(data);
        } catch (error) {
            console.error("Failed to fetch group data:", error);
        }
    }, [group.id]);

    useEffect(() => {
        fetchVersions(true);
    }, [fetchVersions]);

    const handleEditVersion = () => {
        const groupWithSelectedVersion: PromptGroup = {
            ...currentGroup,
            name: selectedVersion?.name || currentGroup.name,
            description: selectedVersion?.description || currentGroup.description,
            category: selectedVersion?.category || currentGroup.category,
            command: selectedVersion?.command || currentGroup.command,
            productionPrompt: selectedVersion
                ? {
                      id: selectedVersion.id,
                      promptText: selectedVersion.promptText,
                      groupId: selectedVersion.groupId,
                      userId: selectedVersion.userId,
                      version: selectedVersion.version,
                      name: selectedVersion.name,
                      description: selectedVersion.description,
                      category: selectedVersion.category,
                      command: selectedVersion.command,
                      createdAt: selectedVersion.createdAt,
                      updatedAt: selectedVersion.updatedAt,
                  }
                : currentGroup.productionPrompt,
            _editingPromptId: selectedVersion?.id,
            _isEditingActiveVersion: selectedVersion?.id === currentGroup.productionPromptId,
            _selectedVersionId: selectedVersion?.id,
        };
        onEdit(groupWithSelectedVersion);
    };

    const handleDeleteVersion = async () => {
        if (!selectedVersion) return;

        const latestVersion = versions.reduce((max, v) => (v.version > max.version ? v : max), versions[0]);
        if (selectedVersion.id === latestVersion?.id) {
            setShowDeleteActiveError(true);
            setTimeout(() => setShowDeleteActiveError(false), 5000);
            return;
        }

        try {
            await api.webui.delete(`/api/v1/prompts/${selectedVersion.id}`);
            addNotification("Version deleted successfully", "success");

            setSelectedVersion(null);
            await fetchVersions(false);
        } catch (error) {
            displayError({ title: "Failed to Delete Version", error: getErrorMessage(error, "An unknown error occurred while deleting the version."), errorSource: "api" });
        }
    };

    const handleRestoreVersion = async () => {
        if (selectedVersion) {
            const latestVersion = versions.reduce((max, v) => (v.version > max.version ? v : max), versions[0]);
            if (selectedVersion.id !== latestVersion?.id) {
                await onRestoreVersion(selectedVersion.id, currentGroup.id);
                await fetchGroupData();
                await fetchVersions(true);
            }
        }
    };

    const latestVersion = versions.length > 0 ? versions.reduce((max, v) => (v.version > max.version ? v : max), versions[0]) : null;
    const isActiveVersion = selectedVersion?.id === latestVersion?.id;

    return (
        <div className="flex h-full flex-col">
            <Header
                title={
                    <>
                        {`Version History: ${currentGroup.name}`} <LifecycleBadge>EXPERIMENTAL</LifecycleBadge>
                    </>
                }
                breadcrumbs={[{ label: "Prompts", onClick: onBack }, { label: "Version History" }]}
                buttons={[
                    ...(isMobile
                        ? [
                              <Button key="toggle-versions" variant="ghost" size="sm" onClick={() => setMobileShowList(prev => !prev)} title="Versions" aria-label="Show versions list">
                                  <List className="h-4 w-4" />
                              </Button>,
                          ]
                        : []),
                    ...(isOwner
                        ? [
                              <DropdownMenu key="actions-menu">
                                  <DropdownMenuTrigger asChild>
                                      <Button variant="ghost" size="sm">
                                          <MoreHorizontal className="h-4 w-4" />
                                      </Button>
                                  </DropdownMenuTrigger>
                                  <DropdownMenuContent align="end">
                                      <DropdownMenuItem onClick={() => onDeleteAll(currentGroup.id, currentGroup.name)}>
                                          <Trash2 size={14} className="mr-2" />
                                          Delete All Versions
                                      </DropdownMenuItem>
                                  </DropdownMenuContent>
                              </DropdownMenu>,
                          ]
                        : []),
                ]}
            />

            {showDeleteActiveError && <MessageBanner variant="error" message="Cannot delete the latest version. To use an older version, restore it to create a new version." />}

            <div className="flex min-h-0 flex-1">
                <div className={cn("overflow-y-auto border-r", isMobile ? cn("w-full border-r-0", !mobileShowList && "hidden") : "w-[300px]")}>
                    <div className="p-4">
                        <h3 className="mb-3 text-sm font-semibold text-(--secondary-text-wMain)">Versions</h3>
                        {isLoading ? (
                            <div className="flex items-center justify-center p-8">
                                <Spinner className="size-6" variant="secondary" />
                            </div>
                        ) : (
                            <div className="space-y-2">
                                {versions.map(version => {
                                    const isActive = version.id === latestVersion?.id;
                                    const isSelected = selectedVersion?.id === version.id;

                                    return (
                                        <button
                                            data-testid={version.id}
                                            key={version.id}
                                            onClick={() => {
                                                setSelectedVersion(version);
                                                if (isMobile) setMobileShowList(false);
                                            }}
                                            className={`w-full p-3 text-left transition-colors ${isSelected ? "bg-(--secondary-w10)" : "hover:bg-(--primary-w10)"}`}
                                        >
                                            <div className="mb-1 flex items-center justify-between">
                                                <span className="text-sm font-medium">Version {version.version}</span>
                                                {isActive && <Badge>Active</Badge>}
                                            </div>
                                            <span className="text-xs text-(--secondary-text-wMain)">{formatPromptDate(version.createdAt)}</span>
                                        </button>
                                    );
                                })}
                            </div>
                        )}
                    </div>
                </div>

                <div className={cn("flex-1 overflow-y-auto", isMobile && mobileShowList && "hidden")}>
                    {selectedVersion ? (
                        <VersionDetailsPanel
                            version={selectedVersion}
                            group={currentGroup}
                            isOwner={isOwner}
                            isViewer={isViewer}
                            isActiveVersion={isActiveVersion}
                            sharingEnabled={sharingEnabled}
                            onEdit={handleEditVersion}
                            onRestore={handleRestoreVersion}
                            onDelete={handleDeleteVersion}
                        />
                    ) : (
                        <div className="flex h-full items-center justify-center">
                            <p className="text-(--secondary-text-wMain)">Select a version to view details</p>
                        </div>
                    )}
                </div>
            </div>
        </div>
    );
};
