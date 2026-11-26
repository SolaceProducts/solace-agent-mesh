// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { NotepadText, Plus, Search } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { api } from "@/api/client";
import { useDisplayName } from "@/api/people";

import { useAuthContext } from "@/providers/contexts";

import { useIsPromptSharingEnabled } from "@/hooks/useIsPromptSharingEnabled";

import { detectVariables } from "@/utils/promptUtils";

import { Badge } from "@/ui/badge";
import { Spinner } from "@/ui/spinner";

import { VariableDialog } from "@/common";
import type { MessageFE, PromptGroup } from "@/types";

export type ChatCommand = "create-template";

interface ReservedCommand {
    command: ChatCommand;
    name: string;
    description: string;
    icon: typeof NotepadText;
}

function PromptAuthorLine({ group }: Readonly<{ group: PromptGroup }>) {
    if (group.authorName) {
        return <p className="mt-1 text-xs text-(--secondary-text-wMain)">{group.authorName}</p>;
    }
    return <PromptAuthorResolvedLine userId={group.userId} />;
}

function PromptAuthorResolvedLine({ userId }: Readonly<{ userId: string }>) {
    const resolved = useDisplayName(userId);
    if (!resolved || resolved === userId) return null;
    return <p className="mt-1 text-xs text-(--secondary-text-wMain)">{resolved}</p>;
}

const RESERVED_COMMANDS: ReservedCommand[] = [
    {
        command: "create-template",
        name: "Create Template from Session",
        description: "Create a reusable prompt template from this conversation",
        icon: NotepadText,
    },
];

interface PromptsCommandProps {
    isOpen: boolean;
    onClose: () => void;
    textAreaRef: React.RefObject<HTMLDivElement | HTMLTextAreaElement | null>;
    onPromptSelect: (promptText: string) => void;
    messages?: MessageFE[];
    onReservedCommand?: (command: ChatCommand, context?: string) => void;
    onBackspaceClose?: () => void;
}

export const PromptsCommand: React.FC<PromptsCommandProps> = ({ isOpen, onClose, textAreaRef, onPromptSelect, messages = [], onReservedCommand, onBackspaceClose }) => {
    const navigate = useNavigate();
    const { userInfo } = useAuthContext();
    const currentUserId = (userInfo?.username as string | undefined)?.toLowerCase() ?? "";
    const sharingEnabled = useIsPromptSharingEnabled();
    const [searchValue, setSearchValue] = useState("");
    const [activeIndex, setActiveIndex] = useState(0);
    const [promptGroups, setPromptGroups] = useState<PromptGroup[]>([]);
    const [isLoading, setIsLoading] = useState(false);
    const [selectedGroup, setSelectedGroup] = useState<PromptGroup | null>(null);
    const [showVariableDialog, setShowVariableDialog] = useState(false);
    const [isKeyboardMode, setIsKeyboardMode] = useState(false);

    const inputRef = useRef<HTMLInputElement>(null);
    const popoverRef = useRef<HTMLDivElement>(null);
    const backdropRef = useRef<HTMLButtonElement>(null);

    useEffect(() => {
        if (!isOpen) return;

        const fetchPromptGroups = async () => {
            setIsLoading(true);
            try {
                const response = await api.webui.get("/api/v1/prompts/groups/all");
                setPromptGroups(response.data ?? []);
            } catch (error) {
                console.error("Failed to fetch prompt groups:", error);
            } finally {
                setIsLoading(false);
            }
        };

        fetchPromptGroups();
    }, [isOpen]);

    const availableReservedCommands = useMemo(() => {
        const hasUserMessages = messages.some(m => m.isUser && !m.isStatusBubble);
        return RESERVED_COMMANDS.filter(cmd => {
            if (cmd.command === "create-template") {
                return hasUserMessages;
            }
            return true;
        });
    }, [messages]);

    const filteredGroups = useMemo(() => {
        const base = searchValue
            ? promptGroups.filter(group => {
                  const search = searchValue.toLowerCase();
                  return group.name.toLowerCase().includes(search) || group.description?.toLowerCase().includes(search) || group.command?.toLowerCase().includes(search) || group.category?.toLowerCase().includes(search);
              })
            : promptGroups;

        return [...base].sort((a, b) => {
            const aCmd = a.command?.toLowerCase() ?? "";
            const bCmd = b.command?.toLowerCase() ?? "";
            if (aCmd && aCmd === bCmd) {
                const aOwner = a.userId.toLowerCase() === currentUserId;
                const bOwner = b.userId.toLowerCase() === currentUserId;
                if (aOwner !== bOwner) return aOwner ? -1 : 1;
            }
            return 0;
        });
    }, [promptGroups, searchValue, currentUserId]);

    const allItems = useMemo(() => {
        return [...filteredGroups, ...availableReservedCommands];
    }, [filteredGroups, availableReservedCommands]);

    const formatSessionHistory = useCallback((messages: MessageFE[]): string => {
        return messages
            .filter(m => !m.isStatusBubble && !m.isError && !m.authenticationLink)
            .map(m => {
                const role = m.isUser ? "User" : "Assistant";
                const text =
                    m.parts
                        ?.filter(p => p.kind === "text")
                        .map(p => (p as { text: string }).text)
                        .join("") || "";
                return `${role}: ${text}`;
            })
            .join("\n\n");
    }, []);

    const handleReservedCommandSelect = useCallback(
        (cmd: ReservedCommand) => {
            if (cmd.command === "create-template" && onReservedCommand) {
                const sessionHistory = formatSessionHistory(messages);
                onReservedCommand(cmd.command, sessionHistory);
                onClose();
                setSearchValue("");
            }
        },
        [messages, formatSessionHistory, onReservedCommand, onClose]
    );

    const handleNavigateToPrompts = useCallback(() => {
        onClose();
        setSearchValue("");
        navigate("/prompts/new?mode=ai-assisted");
    }, [onClose, navigate]);

    const handlePromptSelect = useCallback(
        (group: PromptGroup) => {
            const promptText = group.productionPrompt?.promptText || "";

            const variables = detectVariables(promptText);
            const hasVariables = variables.length > 0;

            if (hasVariables) {
                setSelectedGroup(group);
                setShowVariableDialog(true);
            } else {
                onPromptSelect(promptText);
                onClose();
                setSearchValue("");
            }
        },
        [onPromptSelect, onClose]
    );

    const handleSelect = useCallback(
        (item: ReservedCommand | PromptGroup) => {
            if ("command" in item && RESERVED_COMMANDS.some(cmd => cmd.command === item.command)) {
                handleReservedCommandSelect(item as ReservedCommand);
            } else {
                handlePromptSelect(item as PromptGroup);
            }
        },
        [handleReservedCommandSelect, handlePromptSelect]
    );

    const handleVariableSubmit = useCallback(
        (processedPrompt: string) => {
            onPromptSelect(processedPrompt);
            setShowVariableDialog(false);
            setSelectedGroup(null);
            onClose();
            setSearchValue("");
        },
        [onPromptSelect, onClose]
    );

    const handleKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (e.key === "Escape") {
                e.preventDefault();
                e.stopPropagation();
                onClose();
                setSearchValue("");
                textAreaRef.current?.focus();
            } else if (e.key === "ArrowDown") {
                e.preventDefault();
                setIsKeyboardMode(true);
                setActiveIndex(prev => Math.min(prev + 1, allItems.length - 1));
            } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setIsKeyboardMode(true);
                setActiveIndex(prev => Math.max(prev - 1, 0));
            } else if (e.key === "Enter" || e.key === "Tab") {
                e.preventDefault();
                if (allItems[activeIndex]) {
                    handleSelect(allItems[activeIndex]);
                }
            } else if (e.key === "Backspace" && searchValue === "") {
                if (onBackspaceClose) {
                    onBackspaceClose();
                } else {
                    onClose();
                }
                setSearchValue("");
                textAreaRef.current?.focus();
            }
        },
        [allItems, activeIndex, searchValue, handleSelect, onClose, onBackspaceClose, textAreaRef]
    );

    useEffect(() => {
        if (isOpen && inputRef.current) {
            inputRef.current.focus();
        }
    }, [isOpen]);

    useEffect(() => {
        setActiveIndex(0);
    }, [searchValue]);

    useEffect(() => {
        const activeElement = document.getElementById(`prompt-item-${activeIndex}`);
        if (activeElement) {
            activeElement.scrollIntoView({ behavior: "smooth", block: "nearest" });
        }
    }, [activeIndex]);

    if (!isOpen) return null;

    const renderResults = () => {
        if (isLoading) {
            return (
                <div className="flex items-center justify-center p-8">
                    <Spinner className="size-6" variant="secondary" />
                </div>
            );
        }

        if (allItems.length === 0) {
            return (
                <div className="flex flex-col items-center justify-center gap-4 p-8 text-center">
                    <p className="text-sm text-(--secondary-text-wMain)">{searchValue ? "No prompts found" : "No prompts available."}</p>
                    {!searchValue && (
                        <button onClick={handleNavigateToPrompts} className="inline-flex items-center gap-2 rounded-md bg-(--primary-wMain) px-4 py-2 text-sm font-medium text-(--primary-text-w10) transition-colors hover:bg-(--primary-w90)">
                            <Plus className="size-4" />
                            Create Prompt
                        </button>
                    )}
                </div>
            );
        }

        return (
            <div className="flex flex-col p-2">
                {filteredGroups.map((group, index) => {
                    return (
                        <button
                            key={group.id}
                            id={`prompt-item-${index}`}
                            onClick={() => handlePromptSelect(group)}
                            onMouseEnter={() => {
                                setIsKeyboardMode(false);
                                setActiveIndex(index);
                            }}
                            className={`w-full rounded-xs p-3 text-left transition-colors ${index === activeIndex ? "bg-(--secondary-w10)" : !isKeyboardMode ? "hover:bg-(--primary-w10)" : ""}`}
                        >
                            <div className="flex items-start gap-3">
                                <NotepadText className="mt-0.5 size-4 flex-shrink-0 text-(--secondary-text-wMain)" />
                                <div className="min-w-0 flex-1">
                                    <div className="flex flex-wrap items-center gap-2">
                                        {group.command && <span className="font-mono text-xs text-(--primary-wMain)">/{group.command}</span>}
                                        <span className="text-sm font-medium">{group.name}</span>
                                        {group.category && <Badge>{group.category}</Badge>}
                                    </div>
                                    {group.description && <p className="mt-1 line-clamp-2 text-xs text-(--secondary-text-wMain)">{group.description}</p>}
                                    {sharingEnabled && <PromptAuthorLine group={group} />}
                                </div>
                            </div>
                        </button>
                    );
                })}

                {availableReservedCommands.length > 0 && (
                    <>
                        {filteredGroups.length > 0 && <div className="my-2 border-t" />}
                        {availableReservedCommands.map((cmd, index) => {
                            const actualIndex = filteredGroups.length + index;
                            const Icon = cmd.icon;
                            return (
                                <button
                                    key={`reserved-${cmd.command}`}
                                    id={`prompt-item-${actualIndex}`}
                                    onClick={() => handleReservedCommandSelect(cmd)}
                                    onMouseEnter={() => {
                                        setIsKeyboardMode(false);
                                        setActiveIndex(actualIndex);
                                    }}
                                    className={`w-full rounded-xs p-3 text-left transition-colors ${actualIndex === activeIndex ? "bg-(--secondary-w10)" : !isKeyboardMode ? "hover:bg-(--primary-w10)" : ""}`}
                                >
                                    <div className="flex items-start gap-3">
                                        <Icon className="mt-0.5 size-4 flex-shrink-0 text-(--info-wMain)" />
                                        <div className="min-w-0 flex-1">
                                            <div className="flex flex-wrap items-center gap-2">
                                                <span className="font-mono text-xs text-(--info-wMain)">/{cmd.command}</span>
                                                <span className="text-sm font-medium">{cmd.name}</span>
                                                <Badge variant="outline" className="text-(--info-wMain)">
                                                    Reserved
                                                </Badge>
                                            </div>
                                            <p className="mt-1 line-clamp-2 text-xs text-(--secondary-text-wMain)">{cmd.description}</p>
                                        </div>
                                    </div>
                                </button>
                            );
                        })}
                    </>
                )}
            </div>
        );
    };

    return (
        <>
            <button ref={backdropRef} type="button" tabIndex={-1} aria-label="Close" className="fixed inset-0 z-40 cursor-default bg-black/20" onClick={onClose} />

            <div data-testid="promptCommand" className="fixed top-1/3 left-1/2 z-50 w-full max-w-[672px] -translate-x-1/2 px-4">
                <div ref={popoverRef} className="flex flex-col rounded-md border bg-(--background-w10) shadow-lg" style={{ maxHeight: "60vh" }}>
                    <div className="flex items-center gap-2 border-b p-3">
                        <Search className="size-4 text-(--secondary-text-wMain)" />
                        <input
                            ref={inputRef}
                            type="text"
                            value={searchValue}
                            onChange={e => setSearchValue(e.target.value)}
                            onKeyDown={handleKeyDown}
                            placeholder="Search by shortcut or name..."
                            className="flex-1 bg-transparent text-sm outline-none placeholder:text-(--secondary-text-w50)"
                        />
                    </div>

                    <div className="min-h-0 flex-1 overflow-y-auto">{renderResults()}</div>
                </div>
            </div>

            {showVariableDialog && selectedGroup && (
                <VariableDialog
                    group={selectedGroup}
                    onSubmit={handleVariableSubmit}
                    onClose={() => {
                        setShowVariableDialog(false);
                        setSelectedGroup(null);
                    }}
                />
            )}
        </>
    );
};
