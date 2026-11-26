// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ApiV2Error } from "@/api/common";

import { getErrorMessage } from "@/utils/api";

const FORM_FIELDS = ["name", "description"] as const;

type FormField = (typeof FORM_FIELDS)[number];

export type ToolsetFieldErrors = Partial<Record<FormField, string>>;

const isFormField = (field: string): field is FormField => (FORM_FIELDS as readonly string[]).includes(field);

function findApiV2Error(error: unknown): ApiV2Error | undefined {
    for (let current = error; current instanceof Error; current = current.cause) {
        if (current instanceof ApiV2Error) return current;
    }
    return undefined;
}

export function placeFieldErrors(error: unknown, fallback: string): { fields: ToolsetFieldErrors; dialogMessage?: string } {
    const details = findApiV2Error(error)?.validationDetails ?? {};
    const fields: ToolsetFieldErrors = {};
    const unplaced: string[] = [];
    for (const [field, messages] of Object.entries(details)) {
        if (isFormField(field)) fields[field] = (messages ?? []).join(" ");
        else unplaced.push(`${field}: ${(messages ?? []).join(", ")}`);
    }
    if (Object.keys(fields).length === 0) return { fields, dialogMessage: getErrorMessage(error, fallback) };
    return { fields, dialogMessage: unplaced.length > 0 ? unplaced.join("; ") : undefined };
}
