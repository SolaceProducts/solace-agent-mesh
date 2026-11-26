// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { z } from "zod";

import { gatewaysApi } from "@/api/gateways";

import { matchesConditionalDisplay } from "@/utils/conditionalDisplay";

import type { GatewayFieldSchema, GatewaySchema } from "@/types/models";

import { supportsCustomEndpoint } from "../customEndpoint";
import { acknowledgmentPolicySchema, structuredInvocationSchema } from "../eventRuleSchemas";
import { GATEWAY_TYPE_INFO } from "../GatewayTypeChip";

const eventRulesArraySchema = z
    .array(
        z.object({
            name: z.string().min(1),
            subscriptions: z.array(
                z.object({
                    topic: z.string(),
                })
            ),
            messageFormat: z.enum(["json", "text", "binary"]),
            targetAgent: z.string().optional(),
            promptTemplate: z.string().optional(),
            targetWorkflowName: z.string().optional(),
            runAs: z.string().optional(),
            inputExpression: z.string().optional(),
            payloadEncoding: z.string().optional(),
            artifactProcessing: z.unknown().optional(),
            eventFilter: z.unknown().optional(),
            structuredInvocation: structuredInvocationSchema,
            acknowledgmentPolicy: acknowledgmentPolicySchema,
            successOutput: z.object({
                enabled: z.boolean(),
                topic: z.string(),
                topicType: z.enum(["static", "dynamic"]).optional(),
            }),
            errorOutput: z.object({
                enabled: z.boolean(),
                topic: z.string(),
                topicType: z.enum(["static", "dynamic"]).optional(),
            }),
            defaultUserIdentity: z.string().optional(),
            userIdentityExpression: z.string().optional(),
            forwardContext: z.record(z.string(), z.string()).optional(),
        })
    )
    .min(1, "At least one event rule is required");

const arrayObjectValidator = (fieldSchema: GatewayFieldSchema): z.ZodTypeAny => {
    const itemKey = fieldSchema.itemKey ?? "value";
    const itemLabel = fieldSchema.itemLabel ?? fieldSchema.label;
    const iv = fieldSchema.itemValidation;

    let itemString = z.string({ message: `${itemLabel} must be a string` });
    const minLength = iv?.minLength ?? 1;
    const minMsg = minLength === 1 ? `${itemLabel} is required` : `${itemLabel} must be at least ${minLength} characters`;
    itemString = itemString.min(minLength, minMsg);
    if (iv?.maxLength !== undefined) {
        itemString = itemString.max(iv.maxLength, `${itemLabel} must not exceed ${iv.maxLength} characters`);
    }
    let inner: z.ZodTypeAny = itemString;
    if (iv?.pattern) {
        const pat = new RegExp(iv.pattern);
        inner = itemString.refine(val => pat.test(val), `${itemLabel} format is invalid`);
    }

    let arr: z.ZodTypeAny = z.array(z.object({ [itemKey]: inner }));
    if (fieldSchema.validation?.minItems !== undefined) {
        arr = (arr as z.ZodArray<any>).min(fieldSchema.validation.minItems, `${fieldSchema.label} must have at least ${fieldSchema.validation.minItems} item(s)`);
    }
    if (fieldSchema.validation?.maxItems !== undefined) {
        arr = (arr as z.ZodArray<any>).max(fieldSchema.validation.maxItems, `${fieldSchema.label} must not exceed ${fieldSchema.validation.maxItems} item(s)`);
    }
    return arr;
};

const requiredMessage = (fieldSchema: GatewayFieldSchema): string => fieldSchema.validation?.requiredMsg ?? `${fieldSchema.label} is required`;

const applyNumberValidationConstraints = (fieldSchema: GatewayFieldSchema, validator: z.ZodNumber): z.ZodTypeAny => {
    let v: z.ZodNumber = validator;
    if (fieldSchema.validation?.min !== undefined) {
        v = v.min(fieldSchema.validation.min, `${fieldSchema.label} must be at least ${fieldSchema.validation.min}`);
    }
    if (fieldSchema.validation?.max !== undefined) {
        v = v.max(fieldSchema.validation.max, `${fieldSchema.label} must not exceed ${fieldSchema.validation.max}`);
    }
    return v;
};

const applyStringValidationConstraints = (fieldSchema: GatewayFieldSchema, isEditMode: boolean, validator: z.ZodString): z.ZodTypeAny => {
    let v: z.ZodTypeAny = validator;
    const minLength = fieldSchema.validation?.minLength;
    if (minLength !== undefined) {
        const minMsg = fieldSchema.required && minLength === 1 ? requiredMessage(fieldSchema) : `${fieldSchema.label} must be at least ${minLength} characters`;
        if (isEditMode && fieldSchema.secret) {
            v = (v as z.ZodString).refine(val => val === "" || val.length >= minLength, minMsg);
        } else {
            v = (v as z.ZodString).min(minLength, minMsg);
        }
    }
    if (fieldSchema.validation?.maxLength !== undefined) {
        v = (v as z.ZodString).max(fieldSchema.validation.maxLength, `${fieldSchema.label} must not exceed ${fieldSchema.validation.maxLength} characters`);
    }
    if (fieldSchema.validation?.pattern) {
        const pat = new RegExp(fieldSchema.validation.pattern);
        const patternMsg = fieldSchema.validation.errorMsg ?? `${fieldSchema.label} format is invalid`;
        if (fieldSchema.required) {
            v = (v as z.ZodString).regex(pat, patternMsg);
        } else {
            v = (v as z.ZodString).refine(val => val === "" || pat.test(val), patternMsg);
        }
    }
    return v;
};

const baseValidatorForType = (fieldSchema: GatewayFieldSchema, isEditMode: boolean): z.ZodTypeAny => {
    switch (fieldSchema.type) {
        case "number":
            return applyNumberValidationConstraints(fieldSchema, z.number());
        case "boolean":
            return z.boolean();
        case "event_rules":
            return eventRulesArraySchema;
        case "array<object>":
            return arrayObjectValidator(fieldSchema);
        default: {
            let v = z.string({ message: `${fieldSchema.label} must be a string` });
            if (fieldSchema.required && !fieldSchema.conditionalDisplay && fieldSchema.validation?.minLength === undefined && !(isEditMode && fieldSchema.secret)) {
                v = v.min(1, requiredMessage(fieldSchema));
            }

            return applyStringValidationConstraints(fieldSchema, isEditMode, v);
        }
    }
};

const createFieldValidator = (fieldSchema: GatewayFieldSchema, _isEditMode: boolean = false): z.ZodTypeAny => {
    let validator = baseValidatorForType(fieldSchema, _isEditMode);

    const isConditional = !!fieldSchema.conditionalDisplay;
    const editSecret = _isEditMode && !!fieldSchema.secret;

    if (!fieldSchema.required || isConditional || editSecret) {
        validator = isConditional ? validator.or(z.literal("")).optional() : validator.optional();
        if (editSecret) {
            validator = validator.transform(val => (val === undefined || val === null ? undefined : val));
        }
    }

    return validator;
};

const GATEWAY_SLUG_RESERVED = new Set(["oauth", "api", "well-known", "gw", "health", "evals"]);
const GATEWAY_SLUG_MIN_LENGTH = 3;
const GATEWAY_SLUG_MAX_LENGTH = 63;
const gatewaySlugSchema = z
    .string()
    .optional()
    .refine(s => !s || (s.length >= GATEWAY_SLUG_MIN_LENGTH && s.length <= GATEWAY_SLUG_MAX_LENGTH && /^[a-z][a-z0-9-]*[a-z0-9]$/.test(s) && !GATEWAY_SLUG_RESERVED.has(s)), {
        message: "Custom endpoint must be 3–63 characters: lowercase letters, digits, and dashes; start with a letter and end with a letter or digit; and not be a reserved word (oauth, api, well-known, gw, health, evals).",
    });

export const createFullGatewaySchemaValidator = (gatewaySchema: GatewaySchema, isEditMode: boolean = false): z.ZodObject<any> => {
    const valuesSchemaShape: Record<string, z.ZodTypeAny> = {};

    Object.entries(gatewaySchema.schema).forEach(([fieldName, fieldSchema]) => {
        valuesSchemaShape[fieldName] = createFieldValidator(fieldSchema, isEditMode);
    });

    const valuesSchema = z.object(valuesSchemaShape).superRefine((values, ctx) => {
        Object.entries(gatewaySchema.schema).forEach(([fieldName, fieldSchema]) => {
            if (!fieldSchema.conditionalDisplay) return;
            if (!fieldSchema.required) return;

            const conditionsMet = matchesConditionalDisplay(fieldSchema.conditionalDisplay, values);

            if (conditionsMet) {
                const value = values[fieldName];

                if (isEditMode && fieldSchema.secret) {
                    return;
                }

                if (value === undefined || value === null || value === "") {
                    ctx.addIssue({
                        code: z.ZodIssueCode.custom,
                        message: requiredMessage(fieldSchema),
                        path: [fieldName],
                    });
                }
            }
        });
    });

    const topLevelShape: Record<string, z.ZodTypeAny> = {
        name: z.string().min(3, "Name must be at least 3 characters").max(255, "Name must not exceed 255 characters"),
        description: z.string().min(10, "Description must be at least 10 characters").max(1000, "Description must not exceed 1000 characters"),
        values: valuesSchema,
    };

    if (supportsCustomEndpoint(gatewaySchema.type)) {
        topLevelShape.slug = gatewaySlugSchema;
    }

    return z.object(topLevelShape);
};

export const useGatewaySchemas = ({ enabled }: { enabled: boolean }) => {
    const [gatewaySchemas, setGatewaySchemas] = useState<GatewaySchema[]>([]);
    const [isLoading, setIsLoading] = useState(false);

    const mounted = useRef(true);
    useEffect(() => {
        mounted.current = true;
        return () => {
            mounted.current = false;
        };
    }, []);

    const refetch = useCallback(async () => {
        if (!enabled) return;

        setIsLoading(true);

        try {
            const schemas = await gatewaysApi.getSchemas();

            const enhancedSchemas = schemas.map(schema => ({
                ...schema,
                schemaValidator: createFullGatewaySchemaValidator(schema),
                icon: GATEWAY_TYPE_INFO[schema.type]?.icon ?? null,
            }));

            if (mounted.current) setGatewaySchemas(enhancedSchemas);
        } catch (err) {
            console.error("Failed to load gateway schemas:", err);
        } finally {
            if (mounted.current) setIsLoading(false);
        }
    }, [enabled]);

    useEffect(() => {
        refetch();
    }, [refetch]);

    const getGatewaySchema = useCallback(
        (type: string) => {
            return gatewaySchemas.find(s => s.type === type);
        },
        [gatewaySchemas]
    );

    return { gatewaySchemas, isLoading, getGatewaySchema, refetch };
};
