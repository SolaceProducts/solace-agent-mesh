// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useState } from "react";
import { Eye, Loader2, Pencil, Sparkles } from "lucide-react";

import { useNavigationBlocker } from "@/common/dialogs";

import type { PromptGroup, TemplateConfig } from "@/types/prompts";

import { Header, MessageBanner } from "@/common";
import { useIsMobile, useIsPromptVersionHistoryEnabled } from "@/hooks";
import { Button, CardTitle, HighlightedTextarea, Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Tabs, TabsList, TabsTrigger } from "@/ui";

import { usePromptTemplateBuilder, type ValidationErrors } from "./hooks/usePromptTemplateBuilder";
import { PromptBuilderChat } from "./PromptBuilderChat";
import { TemplatePreviewPanel } from "./TemplatePreviewPanel";
import { usePromptAccess } from "./usePromptAccess";

interface PromptTemplateBuilderProps {
    onBack: () => void;
    onSuccess?: (createdPromptId?: string | null) => void;
    initialMessage?: string | null;
    prefillOnly?: boolean;
    editingGroup?: PromptGroup | null;
    isEditing?: boolean;
    initialMode?: "manual" | "ai-assisted";
}

interface BuilderModeToggleProps {
    builderMode: "manual" | "ai-assisted";
    onSwitchToManual: () => void;
    onSwitchToAI: () => void;
    isEditing: boolean;
}

const BuilderModeToggle: React.FC<BuilderModeToggleProps> = ({ builderMode, onSwitchToManual, onSwitchToAI, isEditing }) => {
    if (builderMode === "ai-assisted") {
        return (
            <Button data-testid="editManuallyButton" onClick={onSwitchToManual} variant="ghost" size="sm">
                <Pencil className="mr-1 h-3 w-3" />
                Edit Manually
            </Button>
        );
    }
    return (
        <Button data-testid="buildWithAIButton" onClick={onSwitchToAI} variant="ghost" size="sm">
            <Sparkles className="mr-1 h-3 w-3" />
            {isEditing ? "Edit with AI" : "Build with AI"}
        </Button>
    );
};

const ViewerAccessDenied: React.FC<{ onBack: () => void }> = ({ onBack }) => (
    <div className="flex h-full flex-col">
        <Header title="Edit Prompt" breadcrumbs={[{ label: "Prompts", onClick: onBack }, { label: "Edit Prompt" }]} />
        <div className="flex flex-1 flex-col items-center justify-center gap-4 px-8 py-6 text-center">
            <p className="text-sm text-(--secondary-text-wMain)">You have view-only access to this prompt. Ask the owner for editor access to make changes.</p>
            <Button onClick={onBack}>Back to Prompts</Button>
        </div>
    </div>
);

interface ManualBuilderFormProps {
    config: TemplateConfig;
    updateConfig: (updates: Partial<TemplateConfig>) => void;
    validationErrors: ValidationErrors;
    commandLocked: boolean;
}

const ManualBuilderForm: React.FC<ManualBuilderFormProps> = ({ config, updateConfig, validationErrors, commandLocked }) => (
    <div className="flex-1 overflow-y-auto px-8 py-6">
        <div className="mx-auto max-w-4xl space-y-6">
            <div>
                <CardTitle className="mb-4 text-base">Basic Information</CardTitle>
                <div className="space-y-6">
                    <div className="space-y-2">
                        <Label htmlFor="template-name">
                            Name <span className="text-(--primary-wMain)">*</span>
                        </Label>
                        <Input
                            id="template-name"
                            placeholder="e.g., Code Review Template"
                            value={config.name || ""}
                            onChange={e => updateConfig({ name: e.target.value })}
                            className={`placeholder:text-(--secondary-text-w50) ${validationErrors.name ? "border-(--error-w100)" : ""}`}
                        />
                        {validationErrors.name && <p className="text-sm text-(--error-wMain)">{validationErrors.name}</p>}
                    </div>

                    <div className="space-y-2">
                        <Label htmlFor="template-description">Description</Label>
                        <Input
                            id="template-description"
                            placeholder="e.g., Reviews code for best practices and potential issues"
                            value={config.description || ""}
                            onChange={e => updateConfig({ description: e.target.value })}
                            className="placeholder:text-(--secondary-text-w50)"
                        />
                    </div>

                    <div className="space-y-2">
                        <Label htmlFor="template-category">Tag</Label>
                        <Select value={config.category || "none"} onValueChange={value => updateConfig({ category: value === "none" ? undefined : value })}>
                            <SelectTrigger>
                                <SelectValue placeholder="Select tag" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="none">No Tag</SelectItem>
                                <SelectItem value="Development">Development</SelectItem>
                                <SelectItem value="Analysis">Analysis</SelectItem>
                                <SelectItem value="Documentation">Documentation</SelectItem>
                                <SelectItem value="Communication">Communication</SelectItem>
                                <SelectItem value="Testing">Testing</SelectItem>
                                <SelectItem value="Other">Other</SelectItem>
                            </SelectContent>
                        </Select>
                    </div>

                    <div className="space-y-2">
                        <Label htmlFor="template-command">
                            Chat Shortcut <span className="text-(--primary-wMain)">*</span>
                        </Label>
                        <div className="flex items-center gap-2">
                            <span className="text-sm text-(--secondary-text-wMain)">/</span>
                            <Input
                                id="template-command"
                                placeholder="e.g., code-review"
                                value={config.command || ""}
                                onChange={e => updateConfig({ command: e.target.value })}
                                disabled={commandLocked}
                                title={commandLocked ? "Only the prompt owner can change the chat shortcut." : undefined}
                                className={`placeholder:text-(--secondary-text-w50) ${validationErrors.command ? "border-(--error-w100)" : ""}`}
                            />
                        </div>
                        <p className="text-xs text-(--secondary-text-wMain)">{commandLocked ? "Only the prompt owner can change the chat shortcut." : "Quick access shortcut for chat (letters, numbers, hyphens, underscores only)"}</p>
                        {validationErrors.command && <p className="text-sm text-(--error-wMain)">{validationErrors.command}</p>}
                    </div>
                </div>
            </div>

            <div>
                <CardTitle className="mb-4 text-base">
                    Content<span className="text-(--primary-wMain)">*</span>
                </CardTitle>
                <div className="space-y-2">
                    <HighlightedTextarea
                        id="template-prompt"
                        data-testid="prompt-text-input"
                        placeholder="Enter your prompt template here. Use {{Variable Name}} for placeholders."
                        value={config.promptText || ""}
                        onChange={e => updateConfig({ promptText: e.target.value })}
                        rows={12}
                        className={`placeholder:text-(--secondary-text-w50) ${validationErrors.promptText ? "border-(--error-w100)" : ""}`}
                    />
                    {validationErrors.promptText && <p className="text-sm text-(--error-wMain)">{validationErrors.promptText}</p>}

                    <div className="space-y-2">
                        <p className="text-sm text-(--secondary-text-wMain)">
                            Variables are placeholder values that make your prompt flexible and reusable. You will be asked to fill in these variable values whenever you use this prompt. Use {`{{Variable Name}}`} for placeholders.
                            {config.detectedVariables && config.detectedVariables.length > 0 && " Your prompt has the following variables:"}
                        </p>
                        {config.detectedVariables && config.detectedVariables.length > 0 && (
                            <div className="flex flex-wrap gap-2">
                                {config.detectedVariables.map((variable, index) => (
                                    <span key={`${index}-${variable}`} className="rounded bg-(--info-w10) px-2 py-1 font-mono text-xs text-(--info-wMain)">
                                        {`{{${variable}}}`}
                                    </span>
                                ))}
                            </div>
                        )}
                    </div>
                </div>
            </div>
        </div>
    </div>
);

export const PromptTemplateBuilder: React.FC<PromptTemplateBuilderProps> = props => {
    const { editingGroup, isEditing = false, onBack } = props;
    const { isViewer } = usePromptAccess(editingGroup);
    if (isEditing && editingGroup != null && isViewer) {
        return <ViewerAccessDenied onBack={onBack} />;
    }
    return <PromptTemplateBuilderForm {...props} />;
};

const PromptTemplateBuilderForm: React.FC<PromptTemplateBuilderProps> = ({ onBack, onSuccess, initialMessage, prefillOnly, editingGroup, isEditing = false, initialMode }) => {
    const { config, updateConfig, saveTemplate, updateTemplate, resetConfig, validationErrors, isLoading } = usePromptTemplateBuilder(editingGroup);
    const { isOwner } = usePromptAccess(editingGroup);
    const commandLocked = isEditing && editingGroup != null && !isOwner;

    const versionHistoryEnabled = useIsPromptVersionHistoryEnabled();

    const [builderMode, setBuilderMode] = useState<"manual" | "ai-assisted">(initialMode || "ai-assisted");
    const isMobile = useIsMobile();
    const [mobileAiPane, setMobileAiPane] = useState<"builder" | "preview">("builder");
    const [isReadyToSave, setIsReadyToSave] = useState(false);
    const [highlightedFields, setHighlightedFields] = useState<string[]>([]);

    const [initialConfig, setInitialConfig] = useState<typeof config | null>(null);
    const { allowNavigation, NavigationBlocker, setBlockingEnabled } = useNavigationBlocker();

    useEffect(() => {
        if (editingGroup && isEditing) {
            const initialData = {
                name: editingGroup.name,
                description: editingGroup.description,
                category: editingGroup.category,
                command: editingGroup.command,
                promptText: editingGroup.productionPrompt?.promptText || "",
            };
            updateConfig(initialData);
            setInitialConfig(initialData);
        } else {
            setInitialConfig({
                name: "",
                description: "",
                category: undefined,
                command: "",
                promptText: "",
            });
        }
    }, [editingGroup, isEditing, updateConfig]);

    useEffect(() => {
        if (!initialConfig) {
            setBlockingEnabled(false);
            return;
        }

        const hasContent = !!(config.name?.trim() || config.description?.trim() || config.category || config.command?.trim() || config.promptText?.trim());

        if (!hasContent) {
            setBlockingEnabled(false);
            return;
        }

        const hasUnsavedChanges =
            config.name !== initialConfig.name || config.description !== initialConfig.description || config.category !== initialConfig.category || config.command !== initialConfig.command || config.promptText !== initialConfig.promptText;

        setBlockingEnabled(hasUnsavedChanges);
    }, [config, initialConfig, setBlockingEnabled]);

    const handleClose = (skipCheck = false) => {
        if (skipCheck) {
            allowNavigation(() => {
                resetConfig();
                setBuilderMode("ai-assisted");
                setIsReadyToSave(false);
                setHighlightedFields([]);
                setInitialConfig(null);
                onBack();
            });
        } else {
            onBack();
        }
    };

    const hasValidationErrors = Object.keys(validationErrors).length > 0;
    const validationErrorMessages = Object.values(validationErrors).filter(Boolean);

    const handleSave = async () => {
        let savedId: string | null | undefined;
        if (editingGroup) {
            const ok = await updateTemplate(editingGroup.id, false);
            savedId = ok ? editingGroup.id : null;
        } else {
            savedId = await saveTemplate();
        }
        if (savedId) {
            allowNavigation(() => {
                handleClose(true);
                onSuccess?.(savedId);
            });
        }
    };

    const handleSaveNewVersion = async () => {
        if (isEditing && editingGroup) {
            const success = await updateTemplate(editingGroup.id, true);
            if (success) {
                allowNavigation(() => {
                    handleClose(true);
                    if (onSuccess) {
                        onSuccess(editingGroup.id);
                    }
                });
            }
        }
    };

    const handleConfigUpdate = (updates: Record<string, unknown>) => {
        const changedFields = Object.keys(updates).filter(key => {
            const oldValue = (config as Record<string, unknown>)[key];
            const newValue = updates[key];

            const normalizedOld = oldValue === undefined || oldValue === null || oldValue === "" ? "" : oldValue;
            const normalizedNew = newValue === undefined || newValue === null || newValue === "" ? "" : newValue;

            return normalizedOld !== normalizedNew;
        });

        updateConfig(updates);
        setHighlightedFields(changedFields);
    };

    const handleSwitchToManual = () => {
        setBuilderMode("manual");
        setHighlightedFields([]);
    };

    const handleSwitchToAI = () => {
        setBuilderMode("ai-assisted");
        setHighlightedFields([]);
    };

    return (
        <div className="flex h-full flex-col">
            <Header
                title={isEditing ? "Edit Prompt" : "Create Prompt"}
                breadcrumbs={[{ label: "Prompts", onClick: () => handleClose() }, { label: isEditing ? "Edit Prompt" : "Create Prompt" }]}
                buttons={[<BuilderModeToggle key="builder-mode-toggle" builderMode={builderMode} onSwitchToManual={handleSwitchToManual} onSwitchToAI={handleSwitchToAI} isEditing={isEditing} />]}
            />

            {hasValidationErrors && (
                <div className="px-8 py-3">
                    <MessageBanner variant="error" message={`Please fix the following errors: ${validationErrorMessages.join(", ")}`} />
                </div>
            )}

            {builderMode === "ai-assisted" && isMobile && (
                <Tabs value={mobileAiPane} onValueChange={v => setMobileAiPane(v as "builder" | "preview")} className="border-b px-4 py-2">
                    <TabsList className="w-full bg-transparent p-0">
                        <TabsTrigger value="builder" className="rounded-none rounded-l-md px-4">
                            <Sparkles className="h-4 w-4 shrink-0" />
                            AI Builder
                        </TabsTrigger>
                        <TabsTrigger value="preview" className="rounded-none rounded-r-md border-l-0 px-4">
                            <Eye className="h-4 w-4 shrink-0" />
                            Preview
                        </TabsTrigger>
                    </TabsList>
                </Tabs>
            )}

            <div className="flex min-h-0 flex-1">
                <div className={`w-full overflow-hidden border-r sm:w-[40%] ${builderMode === "manual" ? "hidden" : ""} ${builderMode === "ai-assisted" && isMobile && mobileAiPane !== "builder" ? "hidden" : ""}`}>
                    <PromptBuilderChat onConfigUpdate={handleConfigUpdate} currentConfig={config} onReadyToSave={setIsReadyToSave} initialMessage={initialMessage} prefillOnly={prefillOnly} isEditing={isEditing} />
                </div>

                {builderMode === "ai-assisted" && (
                    <div className={`w-full overflow-hidden sm:w-[60%] ${isMobile && mobileAiPane !== "preview" ? "hidden" : ""}`}>
                        <TemplatePreviewPanel config={config} highlightedFields={highlightedFields} isReadyToSave={isReadyToSave} />
                    </div>
                )}

                {builderMode === "manual" && <ManualBuilderForm config={config} updateConfig={updateConfig} validationErrors={validationErrors} commandLocked={commandLocked} />}
            </div>
            <NavigationBlocker />
            <div className="flex flex-col gap-2 border-t p-4 sm:flex-row sm:justify-end">
                <Button variant="ghost" onClick={() => handleClose(isEditing)} disabled={isLoading}>
                    {isEditing ? "Discard Changes" : "Cancel"}
                </Button>
                {isEditing && versionHistoryEnabled && (
                    <Button variant="outline" onClick={handleSaveNewVersion} disabled={isLoading}>
                        {isLoading ? (
                            <>
                                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                Saving...
                            </>
                        ) : (
                            "Save New Version"
                        )}
                    </Button>
                )}
                <Button data-testid="createPromptButton" onClick={handleSave} disabled={isLoading}>
                    {isLoading ? (
                        <>
                            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                            {isEditing ? "Saving..." : "Creating..."}
                        </>
                    ) : isEditing ? (
                        "Save"
                    ) : (
                        "Create"
                    )}
                </Button>
            </div>
        </div>
    );
};
