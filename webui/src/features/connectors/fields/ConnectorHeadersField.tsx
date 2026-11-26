// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import { Plus } from "lucide-react";
import { useFormContext } from "react-hook-form";

import { PageLabel, PageLabelWithValue } from "@/common/layout";

import { Button } from "@/ui/button";

import { KeyValuePairList } from "@/common";

import type { FieldProps } from "../ConnectorEdit";

export function ConnectorHeadersField({ fieldPath, fieldConfig, fieldError }: FieldProps) {
    const { getValues, setValue } = useFormContext();

    const handleAddHeader = useCallback(() => {
        const current = getValues(fieldPath) ?? [];
        setValue(fieldPath, [...current, { key: "", value: "" }]);
    }, [fieldPath, getValues, setValue]);

    return (
        <PageLabelWithValue>
            <div className="flex items-center justify-between">
                <div>
                    <PageLabel>{fieldConfig.label}</PageLabel>
                    {fieldConfig.hint && <div className="text-sm text-(--secondary-text-wMain)">{fieldConfig.hint}</div>}
                </div>
                <Button type="button" variant="ghost" size="sm" onClick={handleAddHeader}>
                    <Plus className="mr-2 size-4" />
                    New Header
                </Button>
            </div>
            <KeyValuePairList name={fieldPath} error={fieldError} />
        </PageLabelWithValue>
    );
}
