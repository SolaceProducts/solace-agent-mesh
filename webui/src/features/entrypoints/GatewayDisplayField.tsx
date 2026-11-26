// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { PageLabel, PageLabelWithValue } from "@/common/layout";

import { cn } from "@/utils/cnTailwind";

const NOT_SET = "—";

const joinScalars = (items: unknown[]): string => {
    const parts = items.filter((v): v is string | number => (typeof v === "string" && v !== "") || typeof v === "number").map(String);
    return parts.length > 0 ? parts.join(", ") : NOT_SET;
};

const formatScalar = (val: unknown): string => {
    if (Array.isArray(val)) {
        return joinScalars(val);
    }
    return (typeof val === "string" && val !== "") || typeof val === "number" ? String(val) : NOT_SET;
};

const formatOption = (val: unknown, options?: { value: string; label: string }[]): string => {
    if (options && (typeof val === "string" || typeof val === "number")) {
        const option = options.find(opt => opt.value === val.toString());
        if (option) {
            return option.label;
        }
    }
    return formatScalar(val);
};

const formatArrayObject = (val: unknown, itemKey?: string): string => {
    if (!Array.isArray(val)) {
        return NOT_SET;
    }
    const key = itemKey ?? "value";
    return joinScalars(val.map(entry => (entry && typeof entry === "object" ? (entry as Record<string, unknown>)[key] : entry)));
};

const formatValue = (val: unknown, type?: string, options?: { value: string; label: string }[], itemKey?: string): string => {
    switch (type) {
        case "password":
            return "••••••••";
        case "array<object>":
            return formatArrayObject(val, itemKey);
        case "number":
            return typeof val === "number" && !Number.isNaN(val) ? val.toString() : NOT_SET;
        case "boolean":
            return val ? "True" : "False";
        case "select":
        case "radio":
        case "agent_select":
        case "workflow_select":
            return formatOption(val, options);
        default:
            return formatScalar(val);
    }
};

interface GatewayDisplayFieldProps {
    label: string;
    value: unknown;
    fieldType?: string;
    options?: { value: string; label: string }[];
    itemKey?: string;
    className?: string;
}

export const GatewayDisplayField = ({ label, value, fieldType = "text", options, itemKey, className }: GatewayDisplayFieldProps) => {
    const displayValue = formatValue(value, fieldType, options, itemKey);
    const isNotSet = displayValue === NOT_SET;

    return (
        <PageLabelWithValue className={cn("flex gap-2", className)}>
            <PageLabel>{label}</PageLabel>
            <div className={cn(isNotSet && "text-(--secondary-text-wMain)", fieldType === "textarea" ? "whitespace-pre-wrap" : "break-all")}>{displayValue}</div>
        </PageLabelWithValue>
    );
};
