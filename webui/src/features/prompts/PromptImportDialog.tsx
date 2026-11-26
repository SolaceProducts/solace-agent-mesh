// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useMemo, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";

import type { PromptGroup } from "@/types/prompts";

import { detectTruncationWarnings, formatZodErrors, getPathErrorMessage, hasPathError, PROMPT_FIELD_LIMITS, type PromptImportData, promptImportSchema, type TruncationWarning } from "@/features/prompts/prompt-import.schema";

import { ConfirmationDialog, ErrorLabel, FileUpload, MessageBanner } from "@/common";
import { Input, Label } from "@/ui";

const promptImportFormSchema = z.object({
    name: z.string().min(1, "Name is required").max(PROMPT_FIELD_LIMITS.NAME_MAX, `Name must be ${PROMPT_FIELD_LIMITS.NAME_MAX} characters or less`),
    command: z.string().max(PROMPT_FIELD_LIMITS.COMMAND_MAX, `Chat shortcut must be ${PROMPT_FIELD_LIMITS.COMMAND_MAX} characters or less`).optional().or(z.literal("")),
});

type PromptImportForm = z.infer<typeof promptImportFormSchema>;

interface ConflictInfo {
    hasNameConflict: boolean;
    hasCommandConflict: boolean;
}

interface PromptImportDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onImport: (data: PromptImportData, options: { preserveCommand: boolean; preserveCategory: boolean }) => Promise<void>;
    existingPrompts: PromptGroup[];
}

export const PromptImportDialog: React.FC<PromptImportDialogProps> = ({ open, onOpenChange, onImport, existingPrompts }) => {
    const [selectedFiles, setSelectedFiles] = useState<FileList | null>(null);
    const [importData, setImportData] = useState<PromptImportData | null>(null);
    const [fileError, setFileError] = useState<string | null>(null);
    const [validationErrors, setValidationErrors] = useState<string[]>([]);
    const [truncationWarnings, setTruncationWarnings] = useState<TruncationWarning[]>([]);
    const [isImporting, setIsImporting] = useState(false);

    const [initialNameConflict, setInitialNameConflict] = useState(false);
    const [initialCommandConflict, setInitialCommandConflict] = useState(false);

    const {
        register,
        handleSubmit,
        formState: { errors },
        reset: resetForm,
        setValue,
        watch,
    } = useForm<PromptImportForm>({
        resolver: zodResolver(promptImportFormSchema),
        defaultValues: {
            name: "",
            command: "",
        },
        mode: "onChange",
    });

    const watchedName = watch("name");
    const watchedCommand = watch("command");

    const detectConflicts = useCallback(
        (name: string, command: string): ConflictInfo => {
            const normalizedName = name.trim().toLowerCase();
            const normalizedCommand = command.trim().toLowerCase();

            let hasNameConflict = false;
            let hasCommandConflict = false;

            for (const prompt of existingPrompts) {
                if (normalizedName && prompt.name?.toLowerCase() === normalizedName) {
                    hasNameConflict = true;
                }

                if (normalizedCommand && prompt.command?.toLowerCase() === normalizedCommand) {
                    hasCommandConflict = true;
                }
            }

            return { hasNameConflict, hasCommandConflict };
        },
        [existingPrompts]
    );

    const conflicts = useMemo((): ConflictInfo => {
        if (!importData) {
            return { hasNameConflict: false, hasCommandConflict: false };
        }

        const currentName = watchedName || "";
        const currentCommand = watchedCommand || "";

        return detectConflicts(currentName, currentCommand);
    }, [importData, watchedName, watchedCommand, detectConflicts]);

    const hasConflicts = conflicts.hasNameConflict || conflicts.hasCommandConflict;

    const validateAndParseFile = useCallback(async (file: File): Promise<PromptImportData | null> => {
        setFileError(null);
        setValidationErrors([]);
        setTruncationWarnings([]);
        setImportData(null);

        if (!file.name.endsWith(".json")) {
            setFileError("Please select a JSON file");
            return null;
        }

        if (file.size > 1024 * 1024) {
            setFileError("File size must be less than 1MB");
            return null;
        }

        try {
            const text = await file.text();
            let data: unknown;

            try {
                data = JSON.parse(text);
            } catch {
                setFileError("Failed to parse JSON file. Please ensure it's a valid JSON format.");
                return null;
            }

            const result = promptImportSchema.safeParse(data);

            if (!result.success) {
                const errors = formatZodErrors(result.error);

                if (hasPathError(result.error, "version")) {
                    const versionMessage = getPathErrorMessage(result.error, "version");
                    setFileError(versionMessage || "Invalid version format");
                } else if (errors.length === 1) {
                    setFileError(errors[0]);
                } else {
                    setFileError("The imported prompt has validation errors:");
                    setValidationErrors(errors);
                }
                return null;
            }

            const warnings = detectTruncationWarnings(result.data);
            setTruncationWarnings(warnings);

            return result.data;
        } catch {
            setFileError("Failed to read file. Please try again.");
            return null;
        }
    }, []);

    const checkInitialConflicts = useCallback(
        (data: PromptImportData) => {
            const importedName = data.prompt.name || "";
            const importedCommand = data.prompt.command || "";

            const conflictInfo = detectConflicts(importedName, importedCommand);

            setInitialNameConflict(conflictInfo.hasNameConflict);
            setInitialCommandConflict(conflictInfo.hasCommandConflict);
        },
        [detectConflicts]
    );

    const handleFileChange = async (files: FileList | null) => {
        setSelectedFiles(files);

        if (files && files[0]) {
            const data = await validateAndParseFile(files[0]);

            if (data) {
                setImportData(data);
                setValue("name", data.prompt.name || "");
                setValue("command", data.prompt.command || "");
                checkInitialConflicts(data);
            }
        } else {
            setImportData(null);
            resetForm();
        }
    };

    const handleFileValidation = (files: FileList): { valid: boolean; error?: string } => {
        const file = files[0];

        if (!file.name.endsWith(".json")) {
            return { valid: false, error: "Please select a JSON file" };
        }

        if (file.size > 1024 * 1024) {
            return { valid: false, error: "File size must be less than 1MB" };
        }

        return { valid: true };
    };

    const onSubmit = async (formData: PromptImportForm) => {
        if (!importData) {
            setFileError("Please select a JSON file to import");
            return;
        }

        if (hasConflicts) {
            return;
        }

        setIsImporting(true);
        setFileError(null);

        try {
            const updatedImportData: PromptImportData = {
                ...importData,
                prompt: {
                    ...importData.prompt,
                    name: formData.name,
                    command: formData.command || undefined,
                },
            };

            await onImport(updatedImportData, {
                preserveCommand: !!formData.command,
                preserveCategory: true,
            });

            handleReset();
        } catch (err) {
            setFileError(err instanceof Error ? err.message : "Failed to import prompt");
            throw err;
        } finally {
            setIsImporting(false);
        }
    };

    const handleConfirm = async () => {
        await handleSubmit(onSubmit)();
    };

    const handleReset = () => {
        setSelectedFiles(null);
        setImportData(null);
        setFileError(null);
        setValidationErrors([]);
        setTruncationWarnings([]);
        setInitialNameConflict(false);
        setInitialCommandConflict(false);
        resetForm();
    };

    const handleClose = () => {
        if (!isImporting) {
            handleReset();
            onOpenChange(false);
        }
    };

    const handleCancel = () => {
        if (!isImporting) {
            handleClose();
        }
    };

    const dialogContent = (
        <div className="mt-2 py-2">
            <FileUpload name="promptFile" accept=".json" multiple={false} disabled={isImporting} value={selectedFiles} onChange={handleFileChange} onValidate={handleFileValidation} />

            {fileError && (
                <div className="space-y-2">
                    <MessageBanner variant="error" message={fileError} />
                    {validationErrors.length > 0 && (
                        <div className="rounded-md border border-(--error-w100) bg-(--error-w10) p-3">
                            <ul className="list-inside list-disc space-y-1 text-sm text-(--error-wMain)">
                                {validationErrors.map((err, idx) => (
                                    <li key={idx}>{err}</li>
                                ))}
                            </ul>
                        </div>
                    )}
                </div>
            )}

            {importData && !fileError && (
                <div className="mt-2 py-2">
                    {truncationWarnings.length > 0 && !hasConflicts && (
                        <MessageBanner
                            variant="warning"
                            message={
                                <div className="space-y-2">
                                    <p className="font-medium">Some fields exceed the maximum length and will be truncated:</p>
                                    <ul className="list-inside list-disc space-y-1 text-sm">
                                        {truncationWarnings.map((warning, idx) => (
                                            <li key={idx}>{warning.message}</li>
                                        ))}
                                    </ul>
                                </div>
                            }
                        />
                    )}

                    <div className="flex flex-col gap-4">
                        <h3 className="text-sm font-semibold">Review Prompt</h3>

                        <div className="space-y-1">
                            <Label htmlFor="import-name" className="text-xs text-(--secondary-text-wMain)">
                                Name{conflicts.hasNameConflict && <span className="text-(--error-wMain)">*</span>}
                            </Label>
                            {initialNameConflict ? (
                                <div className="space-y-2">
                                    <Input id="import-name" {...register("name")} className={`${errors.name || conflicts.hasNameConflict ? "border-(--error-w100)" : ""}`} maxLength={PROMPT_FIELD_LIMITS.NAME_MAX} />

                                    {conflicts.hasNameConflict && !errors.name && <ErrorLabel message="Conflict: Already exists. Change name." />}
                                    {errors.name && <ErrorLabel message={errors.name.message} />}
                                </div>
                            ) : (
                                <p className="overflow-wrap-anywhere text-sm break-words">{watchedName}</p>
                            )}
                        </div>

                        <div className="space-y-1">
                            <Label className="text-xs text-(--secondary-text-wMain)">Description</Label>
                            {importData.prompt.description ? <p className="overflow-wrap-anywhere text-sm break-words">{importData.prompt.description}</p> : <p className="text-sm text-(--secondary-text-wMain) italic">No description</p>}
                        </div>

                        <div className="space-y-1">
                            <Label className="text-xs text-(--secondary-text-wMain)">Tag</Label>
                            {importData.prompt.category ? <p className="overflow-wrap-anywhere text-sm break-words">{importData.prompt.category}</p> : <p className="text-sm text-(--secondary-text-wMain) italic">No tag</p>}
                        </div>

                        <div className="space-y-1">
                            <Label htmlFor="import-command" className="text-xs text-(--secondary-text-wMain)">
                                Chat Shortcut{conflicts.hasCommandConflict && <span className="text-(--error-wMain)">*</span>}
                            </Label>
                            {initialCommandConflict ? (
                                <div className="space-y-2">
                                    <div className="flex items-center gap-2">
                                        <span className="text-sm text-(--secondary-text-wMain)">/</span>
                                        <Input
                                            id="import-command"
                                            {...register("command")}
                                            placeholder="e.g., code-review"
                                            className={`flex-1 ${errors.command || conflicts.hasCommandConflict ? "border-(--error-w100)" : ""}`}
                                            maxLength={PROMPT_FIELD_LIMITS.COMMAND_MAX}
                                        />
                                    </div>
                                    {conflicts.hasCommandConflict && !errors.command && <ErrorLabel message="Conflict: Already exists. Change chat shortcut." />}
                                    {errors.command && <ErrorLabel message={errors.command.message} />}
                                </div>
                            ) : watchedCommand ? (
                                <p className="font-mono text-sm break-all">{watchedCommand}</p>
                            ) : (
                                <p className="text-sm text-(--secondary-text-wMain) italic">No chat shortcut</p>
                            )}
                        </div>

                        <div className="space-y-1">
                            <Label className="text-xs text-(--secondary-text-wMain)">Original Author</Label>
                            {importData.prompt.metadata?.authorName ? <p className="overflow-wrap-anywhere text-sm break-words">{importData.prompt.metadata.authorName}</p> : <p className="text-sm text-(--secondary-text-wMain) italic">No author</p>}
                        </div>
                    </div>
                </div>
            )}
        </div>
    );

    return (
        <ConfirmationDialog
            open={open}
            onOpenChange={open => !open && handleClose()}
            title="Import Prompt"
            description="Import a prompt from an exported prompt JSON file."
            content={dialogContent}
            actionLabels={{ confirm: "Import", cancel: "Cancel" }}
            onConfirm={handleConfirm}
            onCancel={handleCancel}
            isLoading={isImporting}
            isEnabled={!!importData && !hasConflicts}
        />
    );
};
