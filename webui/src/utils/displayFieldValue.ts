// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

type SelectOption = { value: string; label: string };

const NOT_SET_PLACEHOLDER = "—";

const stringifyValue = (val: unknown): string => (typeof val === "string" || typeof val === "number" ? String(val) : NOT_SET_PLACEHOLDER);

const formatKeyValue = (val: unknown): string => {
    let pairs: Array<{ key: string; value: string }>;
    if (Array.isArray(val)) {
        pairs = val.filter((p: { key?: string }) => p.key) as Array<{ key: string; value: string }>;
    } else if (typeof val === "object" && val !== null) {
        pairs = Object.entries(val).map(([key, value]) => ({ key, value: String(value) }));
    } else {
        pairs = [];
    }
    return pairs.length > 0 ? pairs.map(p => `${p.key}: ${p.value}`).join(", ") : NOT_SET_PLACEHOLDER;
};

export const formatDisplayFieldValue = (val: unknown, type: string, options?: SelectOption[]): string => {
    switch (type) {
        case "password":
            return "••••••••";

        case "number":
            if (typeof val === "number" && !isNaN(val)) {
                return val.toString();
            }
            return NOT_SET_PLACEHOLDER;

        case "boolean":
            return val ? "True" : "False";

        case "select":
            if (options && val) {
                const option = options.find(opt => opt.value === val);
                return option?.label ?? stringifyValue(val);
            }
            return stringifyValue(val);

        case "key_value":
            return formatKeyValue(val);

        default:
            return stringifyValue(val);
    }
};
