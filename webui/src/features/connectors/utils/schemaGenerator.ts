// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

import { SAFE_NAME_DESCRIPTION, SAFE_NAME_PATTERN, SAFE_TEXT_DESCRIPTION, SAFE_TEXT_PATTERN } from "@/common/form";

import { matchesConditionalDisplay } from "@/utils/conditionalDisplay";

import type { ConnectorFieldSchema, ConnectorSubtypeSchema } from "@/types";

function applyMinLength(schema: z.ZodString, label: string, fieldConfig: ConnectorFieldSchema, isEffectivelyRequired: boolean, isEditMode: boolean): z.ZodString {
    const { validation, secret } = fieldConfig;
    const minLength = validation?.minLength;

    if (minLength === undefined) {
        return isEffectivelyRequired && !(isEditMode && secret) ? schema.min(1, `${label} is required`) : schema;
    }

    if (isEditMode && secret) {
        return schema.refine(val => val === "" || val === undefined || val === null || val.length >= minLength, `${label} must be at least ${minLength} characters`) as unknown as z.ZodString;
    }

    if (isEffectivelyRequired || minLength > 1) {
        const message = isEffectivelyRequired && minLength === 1 ? `${label} is required` : `Minimum length is ${minLength} characters`;
        return schema.min(minLength, message);
    }

    return schema;
}

function applyPattern(schema: z.ZodString, label: string, pattern: string): z.ZodString {
    try {
        const pat = new RegExp(pattern);
        return schema.refine(val => val === "" || pat.test(val), `Invalid format for ${label}`) as unknown as z.ZodString;
    } catch {
        console.warn(`Invalid regex pattern for field ${label}:`, pattern);
        return schema;
    }
}

function createStringSchema(fieldConfig: ConnectorFieldSchema, isEffectivelyRequired: boolean = false, isEditMode: boolean = false): z.ZodString | z.ZodNullable<z.ZodString> {
    const { label, validation } = fieldConfig;

    if (!isEffectivelyRequired && !validation && fieldConfig.default === null) {
        return z.string({ message: `${label} must be a string` }).nullable();
    }

    let schema: z.ZodString = z.string({ message: `${label} must be a string` });

    if (validation?.maxLength !== undefined) {
        schema = schema.max(validation.maxLength, `Maximum length is ${validation.maxLength} characters`);
    }

    schema = applyMinLength(schema, label, fieldConfig, isEffectivelyRequired, isEditMode);

    if (validation?.pattern) {
        schema = applyPattern(schema, label, validation.pattern);
    }

    return schema;
}

function createNumberSchema(fieldConfig: ConnectorFieldSchema): z.ZodType<number> {
    const { label, validation } = fieldConfig;

    let schema = z.union([z.number(), z.nan()]).transform(val => {
        if (isNaN(val)) {
            throw new Error(`${label} must be a number`);
        }
        return val;
    });

    if (validation?.min !== undefined) {
        schema = schema.refine(val => val >= validation.min!, `Minimum value is ${validation.min}`);
    }
    if (validation?.max !== undefined) {
        schema = schema.refine(val => val <= validation.max!, `Maximum value is ${validation.max}`);
    }

    return schema;
}

function createOptionalNumberSchema(fieldConfig: ConnectorFieldSchema): z.ZodType<number | undefined> {
    const { label, validation } = fieldConfig;

    return z
        .union([z.number(), z.nan(), z.string(), z.undefined(), z.null()])
        .transform(val => {
            if (val === "" || val === null || val === undefined || (typeof val === "number" && isNaN(val))) {
                return undefined;
            }

            const num = typeof val === "string" ? Number(val) : val;
            if (isNaN(num)) {
                throw new Error(`${label} must be a number`);
            }

            return num;
        })
        .refine(val => val === undefined || validation?.min === undefined || val >= validation.min, `Minimum value is ${validation?.min}`)
        .refine(val => val === undefined || validation?.max === undefined || val <= validation.max, `Maximum value is ${validation?.max}`)
        .optional();
}

function applyFieldModifiers(schema: z.ZodTypeAny, fieldConfig: ConnectorFieldSchema, isEditMode: boolean = false): z.ZodTypeAny {
    const { required = false, default: defaultValue, type, secret } = fieldConfig;

    let modifiedSchema = schema;

    if (!required || (isEditMode && secret)) {
        modifiedSchema = modifiedSchema.nullable().optional();

        if (isEditMode && secret) {
            modifiedSchema = modifiedSchema.transform(val => (val === undefined || val === null ? undefined : val));
        } else if (type === "string" || type === "password") {
            modifiedSchema = z.preprocess(val => (val === "" ? null : val), modifiedSchema);
        }
    }

    if (defaultValue !== undefined && defaultValue !== null) {
        if (isEditMode && secret) {
            const existingTransform = modifiedSchema;
            modifiedSchema = z.any().transform((val, ctx) => {
                const result = existingTransform.safeParse(val);
                if (!result.success) {
                    result.error.issues.forEach(issue => ctx.addIssue({ ...issue }));
                    return z.NEVER;
                }
                const transformed = result.data;
                if ((val === undefined || val === null) && transformed === undefined) {
                    return defaultValue;
                }
                return transformed;
            });
        } else {
            modifiedSchema = modifiedSchema.default(defaultValue);
        }
    }

    return modifiedSchema;
}

function shouldValidateField(fieldConfig: ConnectorFieldSchema, currentFormValues: Record<string, any>): boolean {
    return matchesConditionalDisplay(fieldConfig.conditionalDisplay, currentFormValues);
}

function createZodSchemaFromField(fieldConfig: ConnectorFieldSchema, currentFormValues: Record<string, any> = {}, isEditMode: boolean = false): z.ZodTypeAny {
    const { type, required = false } = fieldConfig;

    const shouldValidate = shouldValidateField(fieldConfig, currentFormValues);
    const effectivelyRequired = shouldValidate && required;

    let schema: z.ZodTypeAny;

    if (fieldConfig.hidden) {
        return z.any().optional();
    }

    if (!shouldValidate) {
        schema = z.any().optional();
        if (fieldConfig.default !== undefined && fieldConfig.default !== null) {
            schema = schema.default(fieldConfig.default);
        }
        return schema;
    }

    switch (type) {
        case "key_value":
            return z
                .array(z.object({ key: z.string(), value: z.string() }))
                .refine(
                    pairs => {
                        const keys = pairs.filter(p => p.key.trim()).map(p => p.key.trim().toLowerCase());
                        return keys.length === new Set(keys).size;
                    },
                    { message: "Header names must be unique" }
                )
                .optional();
        case "number":
            schema = effectivelyRequired ? createNumberSchema(fieldConfig) : createOptionalNumberSchema(fieldConfig);
            break;
        case "password":
        case "string":
        default:
            schema = createStringSchema(fieldConfig, effectivelyRequired, isEditMode);
            break;
    }

    if (type !== "number") {
        schema = applyFieldModifiers(schema, { ...fieldConfig, required: effectivelyRequired }, isEditMode);
    }

    return schema;
}

export function createConnectorConfigSchema(connectorSchema: ConnectorSubtypeSchema, currentFormValues: Record<string, any> = {}, isEditMode: boolean = false): z.ZodObject<any> {
    const configFields: Record<string, z.ZodTypeAny> = {};

    if (connectorSchema?.fieldSchema) {
        Object.entries(connectorSchema.fieldSchema).forEach(([fieldName, fieldConfig]) => {
            configFields[fieldName] = createZodSchemaFromField(fieldConfig, currentFormValues, isEditMode);
        });
    }

    return z.object(configFields);
}

export const baseConnectorSchema = z.object({
    name: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z.string().min(3, "Name must be at least 3 characters").max(40, "Name must be 40 characters or less").regex(SAFE_NAME_PATTERN, {
                message: SAFE_NAME_DESCRIPTION,
            })
        ),
    description: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .min(10, "Value must be at least 10 characters")
                .max(1000, "Value must be 1000 characters or less")
                .refine(val => SAFE_TEXT_PATTERN.test(val), {
                    message: SAFE_TEXT_DESCRIPTION,
                })
        ),
    resources: z.instanceof(FileList).nullable().optional(),
});

export function createFullConnectorSchemaValidator(connectorSchema: ConnectorSubtypeSchema, currentFormValues: Record<string, any> = {}, isEditMode: boolean = false): z.ZodObject<any> {
    const configSchema = createConnectorConfigSchema(connectorSchema, currentFormValues, isEditMode);

    return baseConnectorSchema.extend({
        values: configSchema,
    });
}

export function sortFieldsByOrder(fields: Record<string, ConnectorFieldSchema>): Array<[string, ConnectorFieldSchema]> {
    const entries = Object.entries(fields);

    const orderedFields = entries.filter(([, field]) => field.order !== undefined);
    const unorderedFields = entries.filter(([, field]) => field.order === undefined);

    orderedFields.sort(([, a], [, b]) => (a.order || 0) - (b.order || 0));

    return [...orderedFields, ...unorderedFields];
}
