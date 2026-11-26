// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface FieldSchemaForDefaults {
    default?: any;
    required?: boolean;
}

export interface FilterOptions {
    filterSentinels?: boolean;
    filterNull?: boolean;
}

export const filterFormValues = (values: Record<string, any>, options: FilterOptions = { filterSentinels: true, filterNull: true }): Record<string, any> => {
    return Object.entries(values).reduce(
        (acc, [key, value]) => {
            const shouldFilter = (options.filterSentinels && value === "<REDACTED>") || (options.filterNull && value === null);

            if (!shouldFilter) {
                acc[key] = value;
            }
            return acc;
        },
        {} as Record<string, any>
    );
};

export const applyDefaultValues = <T extends FieldSchemaForDefaults>(filteredValues: Record<string, any>, schema: Record<string, T>, originalKeys: string[]): Record<string, any> => {
    const result = { ...filteredValues };

    for (const [fieldName, fieldConfig] of Object.entries(schema)) {
        const wasInBackendResponse = originalKeys.includes(fieldName);
        const isMissingFromForm = result[fieldName] === undefined;
        const hasDefault = fieldConfig.default !== undefined;
        const isRequired = fieldConfig.required;

        if (isMissingFromForm && hasDefault && (!wasInBackendResponse || isRequired)) {
            result[fieldName] = fieldConfig.default;
        }
    }

    return result;
};

export const processBackendValuesForForm = <T extends FieldSchemaForDefaults>(backendValues: Record<string, any>, schema: Record<string, T>): Record<string, any> => {
    const originalKeys = Object.keys(backendValues);
    const filteredValues = filterFormValues(backendValues);
    return applyDefaultValues(filteredValues, schema, originalKeys);
};
