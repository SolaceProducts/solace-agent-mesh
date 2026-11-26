// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

export const PROMPT_FIELD_LIMITS = {
    NAME_MAX: 255,
    DESCRIPTION_MAX: 1000,
    TAG_MAX: 100,
    COMMAND_MAX: 50,
    AUTHOR_NAME_MAX: 255,
    PROMPT_TEXT_MAX: 10000,
} as const;

const promptMetadataSchema = z.object({
    authorName: z.union([z.string(), z.null()]).optional(),
    originalVersion: z.number().int().positive(),
    originalCreatedAt: z.number().int().positive(),
});

const optionalString = () => z.union([z.string(), z.null()]).optional();

const promptDataSchema = z.object({
    name: z.string().min(1, "Name is required"),
    description: optionalString(),
    category: optionalString(),
    command: optionalString(),
    promptText: z.string().min(1, "Prompt text is required"),
    metadata: z.union([promptMetadataSchema, z.null()]).optional(),
});

const exportedAtFromEitherBackend = z.union([z.string().min(1), z.number().int().positive()], {
    message: "Export timestamp must be a non-empty string or a positive number",
});

export const promptImportSchema = z.object({
    version: z.literal("1.0", {
        message: "Unsupported export format version. Only version 1.0 is currently supported.",
    }),
    exportedAt: exportedAtFromEitherBackend,
    prompt: promptDataSchema,
});

export const promptImportCommandSchema = z.object({
    command: z.string().max(PROMPT_FIELD_LIMITS.COMMAND_MAX, `Command must be ${PROMPT_FIELD_LIMITS.COMMAND_MAX} characters or less`).optional().or(z.literal("")),
});

export type PromptImportData = z.infer<typeof promptImportSchema>;
export type PromptImportCommandForm = z.infer<typeof promptImportCommandSchema>;

export interface TruncationWarning {
    field: string;
    currentLength: number;
    maxLength: number;
    message: string;
}

export function detectTruncationWarnings(data: PromptImportData): TruncationWarning[] {
    const warnings: TruncationWarning[] = [];
    const prompt = data.prompt;

    if (prompt.name && prompt.name.length > PROMPT_FIELD_LIMITS.NAME_MAX) {
        warnings.push({
            field: "Name",
            currentLength: prompt.name.length,
            maxLength: PROMPT_FIELD_LIMITS.NAME_MAX,
            message: `Name will be truncated from ${prompt.name.length} to ${PROMPT_FIELD_LIMITS.NAME_MAX} characters`,
        });
    }

    if (prompt.description && prompt.description.length > PROMPT_FIELD_LIMITS.DESCRIPTION_MAX) {
        warnings.push({
            field: "Description",
            currentLength: prompt.description.length,
            maxLength: PROMPT_FIELD_LIMITS.DESCRIPTION_MAX,
            message: `Description will be truncated from ${prompt.description.length} to ${PROMPT_FIELD_LIMITS.DESCRIPTION_MAX} characters`,
        });
    }

    if (prompt.category && prompt.category.length > PROMPT_FIELD_LIMITS.TAG_MAX) {
        warnings.push({
            field: "Tag",
            currentLength: prompt.category.length,
            maxLength: PROMPT_FIELD_LIMITS.TAG_MAX,
            message: `Tag will be truncated from ${prompt.category.length} to ${PROMPT_FIELD_LIMITS.TAG_MAX} characters`,
        });
    }

    if (prompt.command && prompt.command.length > PROMPT_FIELD_LIMITS.COMMAND_MAX) {
        warnings.push({
            field: "Command",
            currentLength: prompt.command.length,
            maxLength: PROMPT_FIELD_LIMITS.COMMAND_MAX,
            message: `Command will be truncated from ${prompt.command.length} to ${PROMPT_FIELD_LIMITS.COMMAND_MAX} characters`,
        });
    }

    if (prompt.promptText && prompt.promptText.length > PROMPT_FIELD_LIMITS.PROMPT_TEXT_MAX) {
        warnings.push({
            field: "Prompt text",
            currentLength: prompt.promptText.length,
            maxLength: PROMPT_FIELD_LIMITS.PROMPT_TEXT_MAX,
            message: `Prompt text will be truncated from ${prompt.promptText.length.toLocaleString()} to ${PROMPT_FIELD_LIMITS.PROMPT_TEXT_MAX.toLocaleString()} characters`,
        });
    }

    return warnings;
}

export function formatZodErrors(error: z.ZodError): string[] {
    return error.issues.map(issue => {
        const path = issue.path.join(".");
        return path ? `${path}: ${issue.message}` : issue.message;
    });
}

export function hasPathError(error: z.ZodError, pathSegment: string): boolean {
    return error.issues.some(issue => issue.path.includes(pathSegment));
}

export function getPathErrorMessage(error: z.ZodError, pathSegment: string): string | undefined {
    const issue = error.issues.find(issue => issue.path.includes(pathSegment));
    return issue?.message;
}
