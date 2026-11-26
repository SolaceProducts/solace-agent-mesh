// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { PageLabel, PageLabelWithValue } from "@/common/layout";

import { cn } from "@/utils/cnTailwind";
import { formatDisplayFieldValue } from "@/utils/displayFieldValue";

interface ToolsetDisplayFieldProps {
    label: string;
    value: any;
    fieldType?: string;
    options?: Array<{ value: string; label: string }>;
    className?: string;
    fieldName?: string;
}

export const ToolsetDisplayField: React.FC<ToolsetDisplayFieldProps> = ({ label, value, fieldType = "text", options, className, fieldName }) => {
    const displayValue = formatDisplayFieldValue(value, fieldType, options);
    const isNotSet = displayValue === "—";
    const testId = fieldName ? `toolset-review-${fieldName.replace(/\./g, "-")}` : undefined;

    return (
        <PageLabelWithValue className={cn("flex gap-2", className)}>
            <PageLabel>{label}</PageLabel>
            <div className={cn(isNotSet && "text-(--secondary-text-wMain)", fieldType === "textarea" ? "whitespace-pre-wrap" : "break-all")} data-testid={testId}>
                {displayValue}
            </div>
        </PageLabelWithValue>
    );
};
