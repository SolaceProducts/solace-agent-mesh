// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function matchesConditionalDisplay(rule: Record<string, string | string[]> | undefined, formValues: Record<string, unknown> | undefined | null): boolean {
    if (!rule) {
        return true;
    }
    return Object.entries(rule).every(([conditionField, conditionValue]) => {
        const actual = formValues?.[conditionField];
        if (Array.isArray(conditionValue)) {
            return conditionValue.includes(actual as string);
        }
        return actual === conditionValue;
    });
}

export function valuesWithSchemaDefaults(values: Record<string, unknown> | undefined | null, schema: Record<string, { default?: unknown }> | undefined | null): Record<string, unknown> {
    const resolved: Record<string, unknown> = { ...values };
    for (const [field, fieldSchema] of Object.entries(schema ?? {})) {
        if (fieldSchema?.default === undefined) {
            continue;
        }
        const current = resolved[field];
        if (current === undefined || current === null || current === "") {
            resolved[field] = fieldSchema.default;
        }
    }
    return resolved;
}
