// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useState } from "react";

import { cn } from "@/utils/cnTailwind";

import { Textarea } from "@/ui/textarea";

import { PageLabel } from "../layout/PageLabel";
import { PageLabelWithValue } from "../layout/PageLabelWithValue";
import { ErrorLabel } from "../status/ErrorLabel";

export interface JsonSchemaFieldProps {
    label: string;
    field: { value: unknown; onChange: (value: unknown) => void };
    readOnly?: boolean;
}

const serializeFieldValue = (value: unknown): string => {
    if (!value) return "";
    return typeof value === "string" ? value : JSON.stringify(value, null, 2);
};

export function JsonSchemaField({ label, field, readOnly }: JsonSchemaFieldProps) {
    const { value: fieldValue, onChange: fieldOnChange } = field;
    const [rawText, setRawText] = useState(() => serializeFieldValue(fieldValue));
    const [jsonError, setJsonError] = useState<string | null>(null);

    const handleChange = useCallback((e: React.ChangeEvent<HTMLTextAreaElement>) => {
        setRawText(e.target.value);
    }, []);

    const handleBlur = useCallback(() => {
        if (!rawText) {
            setJsonError(null);
            fieldOnChange(undefined);
            return;
        }
        try {
            const parsed = JSON.parse(rawText);
            if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
                setJsonError("Schema must be a JSON object");
                fieldOnChange(rawText);
            } else {
                setJsonError(null);
                fieldOnChange(parsed);
            }
        } catch {
            setJsonError("Invalid JSON");
            fieldOnChange(rawText);
        }
    }, [rawText, fieldOnChange]);

    useEffect(() => {
        const serialized = serializeFieldValue(fieldValue);
        setRawText(prev => {
            try {
                if (prev && JSON.stringify(JSON.parse(prev), null, 2) === serialized) return prev;
            } catch {}
            return serialized;
        });
        if (!fieldValue || (typeof fieldValue === "object" && !Array.isArray(fieldValue))) {
            setJsonError(null);
        }
    }, [fieldValue]);

    return (
        <PageLabelWithValue>
            <PageLabel>{label}</PageLabel>
            <Textarea readOnly={readOnly} rows={6} className={cn("font-mono text-sm", jsonError && "border-(--error-w100)")} placeholder='{"type": "object", "properties": {...}}' value={rawText} onChange={handleChange} onBlur={handleBlur} />
            {jsonError && <ErrorLabel message={jsonError} />}
        </PageLabelWithValue>
    );
}
