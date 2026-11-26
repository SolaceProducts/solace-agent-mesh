// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";
import { RefreshCcw, Upload } from "lucide-react";
import { useLoaderData, useLocation, useNavigate } from "react-router-dom";

import { api } from "@/api/client";

import { useChatContext } from "@/providers/contexts";

import { Header } from "@/common/layout/Header";
import { PageLayout } from "@/common/layout/PageLayout";
import { EmptyState } from "@/common/status/EmptyState";

import { Button } from "@/ui/button";
import type { Prompt, PromptGroup } from "@/types/prompts";

import type { PromptImportData } from "@/features/prompts/prompt-import.schema";

import { VariableDialog } from "@/common";
import { useIsPromptSharingEnabled } from "@/hooks";
import { detectVariables, downloadBlob, getErrorMessage } from "@/utils";

import { GeneratePromptDialog } from "./GeneratePromptDialog";
import { consumeNewPromptSeed } from "./promptBuilderInitial";
import { PromptCards } from "./PromptCards";
import { PromptDeleteDialog } from "./PromptDeleteDialog";
import { PromptImportDialog } from "./PromptImportDialog";
import { PromptTemplateBuilder } from "./PromptTemplateBuilder";
import { SharePromptDialog } from "./SharePromptDialog";
import { VersionHistoryPage } from "./VersionHistoryPage";

export const PromptsPage: React.FC = () => {
    const navigate = useNavigate();
    const location = useLocation();
    const loaderData = useLoaderData<{ promptId?: string; view?: string; mode?: string }>();

    const { addNotification, displayError } = useChatContext();
    const [promptGroups, setPromptGroups] = useState<PromptGroup[]>([]);
    const [isLoading, setIsLoading] = useState(false);
    const [showBuilder, setShowBuilder] = useState(false);
    const [showGenerateDialog, setShowGenerateDialog] = useState(false);
    const [initialMessage, setInitialMessage] = useState<string | null>(null);
    const [prefillOnly, setPrefillOnly] = useState(false);
    const [editingGroup, setEditingGroup] = useState<PromptGroup | null>(null);
    const [builderInitialMode, setBuilderInitialMode] = useState<"manual" | "ai-assisted">("ai-assisted");
    const [builderKey, setBuilderKey] = useState(0);
    const [versionHistoryGroup, setVersionHistoryGroup] = useState<PromptGroup | null>(null);
    const [deletingPrompt, setDeletingPrompt] = useState<{ id: string; name: string } | null>(null);
    const [newlyCreatedPromptId, setNewlyCreatedPromptId] = useState<string | null>(null);
    const [showVariableDialog, setShowVariableDialog] = useState(false);
    const [pendingPromptGroup, setPendingPromptGroup] = useState<PromptGroup | null>(null);
    const [showImportDialog, setShowImportDialog] = useState(false);
    const [sharingPrompt, setSharingPrompt] = useState<PromptGroup | null>(null);
    const sharingEnabled = useIsPromptSharingEnabled();

    const fetchPromptGroups = useCallback(async () => {
        setIsLoading(true);
        try {
            const response = await api.webui.get("/api/v1/prompts/groups/all");
            setPromptGroups(response.data ?? []);
        } catch (error) {
            displayError({ title: "Failed to Load Prompts", error: getErrorMessage(error, "An error occurred while fetching prompt groups."), errorSource: "api" });
        } finally {
            setIsLoading(false);
        }
    }, [displayError]);

    useEffect(() => {
        fetchPromptGroups();
    }, [fetchPromptGroups]);

    useEffect(() => {
        if (loaderData?.view === "builder") {
            if (loaderData.mode === "edit" && loaderData.promptId) {
                const loadPromptForEdit = async () => {
                    try {
                        const data = await api.webui.get(`/api/v1/prompts/groups/${loaderData.promptId}`);
                        setEditingGroup(data);
                        setBuilderInitialMode("ai-assisted");
                        setShowBuilder(true);
                    } catch (error) {
                        displayError({ title: "Failed to Edit Prompt", error: getErrorMessage(error, "An error occurred while fetching prompt."), errorSource: "api" });
                    }
                };
                loadPromptForEdit();
            } else {
                setEditingGroup(null);
                const mode = loaderData.mode === "ai-assisted" ? "ai-assisted" : "manual";
                setBuilderInitialMode(mode);

                const { initialMessage: seed, prefillOnly: prefill } = consumeNewPromptSeed(mode, location.state?.taskDescription);
                setInitialMessage(seed);
                setPrefillOnly(prefill);

                setBuilderKey(prev => prev + 1);
                setShowBuilder(true);
            }
        } else if (loaderData?.view === "versions" && loaderData.promptId) {
            const loadPromptGroup = async () => {
                try {
                    const data = await api.webui.get(`/api/v1/prompts/groups/${loaderData.promptId}`);
                    setVersionHistoryGroup(data);
                } catch (error) {
                    displayError({ title: "Failed to View Versions", error: getErrorMessage(error, "An error occurred while fetching versions."), errorSource: "api" });
                }
            };
            loadPromptGroup();
        } else {
            setShowBuilder(false);
            setVersionHistoryGroup(null);
            setEditingGroup(null);
        }
    }, [loaderData, location.state?.taskDescription, displayError]);

    const handleDeleteClick = (id: string, name: string) => {
        setDeletingPrompt({ id, name });
    };

    const handleDeleteConfirm = async () => {
        if (!deletingPrompt) return;

        try {
            await api.webui.delete(`/api/v1/prompts/groups/${deletingPrompt.id}`);
            if (versionHistoryGroup?.id === deletingPrompt.id) {
                setVersionHistoryGroup(null);
            }
            await fetchPromptGroups();
            setDeletingPrompt(null);
            addNotification("Prompt deleted", "success");
        } catch (error) {
            setDeletingPrompt(null);
            displayError({ title: "Failed to Delete Prompt", error: getErrorMessage(error, "An error occurred while deleting the prompt."), errorSource: "api" });
        }
    };

    const handleEdit = (group: PromptGroup) => {
        navigate(`/prompts/${group.id}/edit`);
    };

    const handleRestoreVersion = async (promptId: string, groupId: string) => {
        try {
            const versionsResponse = await api.webui.get(`/api/v1/prompts/groups/${groupId}/prompts`);
            const versions: Prompt[] = versionsResponse.data ?? [];
            const prompt = versions.find(v => v.id === promptId);

            if (!prompt) {
                throw new Error("Prompt version not found");
            }

            await api.webui.patch(`/api/v1/prompts/groups/${groupId}`, {
                initialPrompt: prompt.promptText,
                name: prompt.name,
                description: prompt.description,
                category: prompt.category,
                command: prompt.command,
            });

            fetchPromptGroups();
            addNotification("Version restored as new version", "success");
        } catch (error) {
            displayError({ title: "Failed to Restore Version", error: getErrorMessage(error, "An error occurred while restoring the version."), errorSource: "api" });
        }
    };

    const handleGeneratePrompt = (taskDescription: string) => {
        setShowGenerateDialog(false);
        navigate("/prompts/new?mode=ai-assisted", {
            state: { taskDescription },
        });
    };

    const handleUseInChat = (prompt: PromptGroup) => {
        const promptText = prompt.productionPrompt?.promptText || "";

        const variables = detectVariables(promptText);
        const hasVariables = variables.length > 0;

        if (hasVariables) {
            setPendingPromptGroup(prompt);
            setShowVariableDialog(true);
        } else {
            navigate("/chat", {
                state: {
                    promptText,
                    groupId: prompt.id,
                    groupName: prompt.name,
                },
            });
        }
    };

    const handleVariableSubmit = (processedPrompt: string) => {
        if (!pendingPromptGroup) return;

        navigate("/chat", {
            state: {
                promptText: processedPrompt,
                groupId: pendingPromptGroup.id,
                groupName: pendingPromptGroup.name,
            },
        });

        setShowVariableDialog(false);
        setPendingPromptGroup(null);
    };

    const handleTogglePin = async (id: string, currentStatus: boolean) => {
        try {
            setPromptGroups(prev => prev.map(p => (p.id === id ? { ...p, isPinned: !currentStatus } : p)));

            await api.webui.patch(`/api/v1/prompts/groups/${id}/pin`);
        } catch (error) {
            setPromptGroups(prev => prev.map(p => (p.id === id ? { ...p, isPinned: currentStatus } : p)));
            displayError({ title: "Failed to Update Pin Status", error: getErrorMessage(error, "An error occurred while updating the pin status."), errorSource: "api" });
        }
    };

    const handleExport = async (prompt: PromptGroup) => {
        try {
            const data = await api.webui.get(`/api/v1/prompts/groups/${prompt.id}/export`);

            const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
            const filename = `prompt-${prompt.name.replace(/[^a-z0-9]/gi, "-").toLowerCase()}-${Date.now()}.json`;
            downloadBlob(blob, filename);

            addNotification("Prompt exported successfully", "success");
        } catch (error) {
            displayError({ title: "Failed to Export Prompt", error: getErrorMessage(error, "An error occurred while exporting the prompt."), errorSource: "api" });
        }
    };

    const handleImport = async (importData: PromptImportData, options: { preserveCommand: boolean; preserveCategory: boolean }) => {
        try {
            const apiPayload = {
                promptData: {
                    version: importData.version,
                    exportedAt: importData.exportedAt,
                    prompt: {
                        name: importData.prompt.name,
                        description: importData.prompt.description,
                        category: importData.prompt.category,
                        command: importData.prompt.command,
                        promptText: importData.prompt.promptText,
                        metadata: importData.prompt.metadata
                            ? {
                                  authorName: importData.prompt.metadata.authorName,
                                  originalVersion: importData.prompt.metadata.originalVersion,
                                  originalCreatedAt: importData.prompt.metadata.originalCreatedAt,
                              }
                            : undefined,
                    },
                },
                options: {
                    preserveCommand: options.preserveCommand,
                    preserveCategory: options.preserveCategory,
                },
            };

            const result = await api.webui.post("/api/v1/prompts/import", apiPayload);

            setShowBuilder(false);
            setShowImportDialog(false);
            setInitialMessage(null);
            setEditingGroup(null);

            await fetchPromptGroups();
            setNewlyCreatedPromptId(result.promptGroupId);

            const nonTruncationWarnings = (result.warnings || []).filter((warning: string) => !warning.toLowerCase().includes("truncated"));

            if (nonTruncationWarnings.length > 0) {
                const warningText = nonTruncationWarnings.length === 1 ? nonTruncationWarnings[0] : nonTruncationWarnings.join("; ");
                addNotification(`Prompt imported with notes: ${warningText}`, "info");
            } else {
                addNotification("Prompt imported", "success");
            }
        } catch (error) {
            console.error("Failed to import prompt:", error);
            throw error;
        }
    };

    if (showBuilder) {
        return (
            <>
                <PromptTemplateBuilder
                    key={`builder-${builderKey}-${editingGroup?.id || "new"}`}
                    onBack={() => {
                        navigate("/prompts");
                    }}
                    onSuccess={async (createdPromptId?: string | null) => {
                        if (createdPromptId) {
                            setNewlyCreatedPromptId(createdPromptId);
                        }

                        await fetchPromptGroups();
                        navigate("/prompts");
                    }}
                    initialMessage={initialMessage}
                    prefillOnly={prefillOnly}
                    editingGroup={editingGroup}
                    isEditing={!!editingGroup}
                    initialMode={builderInitialMode}
                />

                {deletingPrompt && <PromptDeleteDialog key={`delete-${deletingPrompt.id}`} isOpen={true} onClose={() => setDeletingPrompt(null)} onConfirm={handleDeleteConfirm} promptId={deletingPrompt.id} promptName={deletingPrompt.name} />}
                <GeneratePromptDialog isOpen={showGenerateDialog} onClose={() => setShowGenerateDialog(false)} onGenerate={handleGeneratePrompt} />
            </>
        );
    }

    if (versionHistoryGroup) {
        return (
            <>
                <VersionHistoryPage group={versionHistoryGroup} onBack={() => navigate("/prompts")} onBackToPromptDetail={() => navigate("/prompts")} onEdit={handleEdit} onDeleteAll={handleDeleteClick} onRestoreVersion={handleRestoreVersion} />

                {deletingPrompt && <PromptDeleteDialog key={`delete-${deletingPrompt.id}`} isOpen={true} onClose={() => setDeletingPrompt(null)} onConfirm={handleDeleteConfirm} promptId={deletingPrompt.id} promptName={deletingPrompt.name} />}
            </>
        );
    }

    return (
        <PageLayout>
            <Header
                title="Prompts"
                buttons={[
                    <Button key="importPrompt" variant="ghost" title="Import Prompt" onClick={() => setShowImportDialog(true)}>
                        <Upload className="size-4" />
                        <span className="hidden sm:inline">Import Prompt</span>
                    </Button>,
                    <Button key="refreshPrompts" data-testid="refreshPrompts" disabled={isLoading} variant="ghost" title="Refresh Prompts" onClick={() => fetchPromptGroups()}>
                        <RefreshCcw className="size-4" />
                        <span className="hidden sm:inline">Refresh Prompts</span>
                    </Button>,
                ]}
            />

            {isLoading ? (
                <EmptyState title="Loading prompts..." variant="loading" />
            ) : (
                <div className="relative flex-1 p-4">
                    <PromptCards
                        prompts={promptGroups}
                        onManualCreate={() => navigate("/prompts/new?mode=manual")}
                        onAIAssisted={() => setShowGenerateDialog(true)}
                        onEdit={handleEdit}
                        onDelete={handleDeleteClick}
                        onViewVersions={group => navigate(`/prompts/${group.id}/versions`)}
                        onUseInChat={handleUseInChat}
                        onTogglePin={handleTogglePin}
                        onExport={handleExport}
                        onShare={sharingEnabled ? setSharingPrompt : undefined}
                        newlyCreatedPromptId={newlyCreatedPromptId}
                    />
                </div>
            )}

            {deletingPrompt && <PromptDeleteDialog key={`delete-${deletingPrompt.id}`} isOpen={true} onClose={() => setDeletingPrompt(null)} onConfirm={handleDeleteConfirm} promptId={deletingPrompt.id} promptName={deletingPrompt.name} />}

            {sharingEnabled && sharingPrompt && <SharePromptDialog isOpen={true} onClose={() => setSharingPrompt(null)} promptGroup={sharingPrompt} />}

            <GeneratePromptDialog isOpen={showGenerateDialog} onClose={() => setShowGenerateDialog(false)} onGenerate={handleGeneratePrompt} />

            {showVariableDialog && pendingPromptGroup && (
                <VariableDialog
                    group={pendingPromptGroup}
                    onSubmit={handleVariableSubmit}
                    onClose={() => {
                        setShowVariableDialog(false);
                        setPendingPromptGroup(null);
                    }}
                />
            )}

            <PromptImportDialog open={showImportDialog} onOpenChange={setShowImportDialog} onImport={handleImport} existingPrompts={promptGroups} />
        </PageLayout>
    );
};
