// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

import { PasswordInput } from "@/common";

import type { AuthStoredSecrets } from "./authSecretTracking";
import { hasStoredAuthSecret } from "./authSecretTracking";
import type { SelectOption } from "./FormComponents";
import { FormField, TextInput } from "./FormComponents";

export const AUTH_TYPES = {
    NONE: "none",
    BEARER: "bearer",
    APIKEY: "apikey",
    OAUTH2: "oauth2",
} as const;

export const AUTH_TYPE_LABELS: Record<string, string> = {
    [AUTH_TYPES.NONE]: "No Authentication",
    [AUTH_TYPES.BEARER]: "Static Bearer Token",
    [AUTH_TYPES.APIKEY]: "Static API Key",
    [AUTH_TYPES.OAUTH2]: "OAuth 2.0 Client Credentials",
};

export const VALIDATION_LIMITS = {
    TOKEN_MIN: 8,
    TOKEN_MAX: 512,
    CLIENT_ID_MAX: 255,
    CLIENT_SECRET_MAX: 512,
} as const;

export const AUTH_TYPE_OPTIONS: SelectOption[] = [
    {
        value: AUTH_TYPES.NONE,
        label: AUTH_TYPE_LABELS[AUTH_TYPES.NONE],
        description: "No authentication required",
    },
    {
        value: AUTH_TYPES.BEARER,
        label: AUTH_TYPE_LABELS[AUTH_TYPES.BEARER],
        description: "Uses Authorization: Bearer header",
    },
    {
        value: AUTH_TYPES.APIKEY,
        label: AUTH_TYPE_LABELS[AUTH_TYPES.APIKEY],
        description: "Uses X-API-Key header (fixed)",
    },
    {
        value: AUTH_TYPES.OAUTH2,
        label: AUTH_TYPE_LABELS[AUTH_TYPES.OAUTH2],
        description: "Fetches tokens automatically from OAuth server",
    },
];

const createSecretValidation = (fieldName: string, minLength: number, maxLength: number) => {
    return z.string().refine(
        val => {
            if (!val || val === "") return true;
            return val.length >= minLength && val.length <= maxLength;
        },
        {
            message: `${fieldName} must be ${minLength}-${maxLength} characters (or leave unchanged to keep existing value)`,
        }
    );
};

export const createAuthValidationSchema = (authType?: string, fieldPrefix: string = "auth", isEditMode: boolean = false) => {
    const schemas: Record<string, any> = {};

    if (authType === AUTH_TYPES.BEARER) {
        if (isEditMode) {
            schemas[`${fieldPrefix}BearerToken`] = createSecretValidation("Bearer token", VALIDATION_LIMITS.TOKEN_MIN, VALIDATION_LIMITS.TOKEN_MAX);
        } else {
            schemas[`${fieldPrefix}BearerToken`] = z.string().min(VALIDATION_LIMITS.TOKEN_MIN, `Bearer token must be at least ${VALIDATION_LIMITS.TOKEN_MIN} characters`).max(VALIDATION_LIMITS.TOKEN_MAX, "Bearer token too long");
        }
    }

    if (authType === AUTH_TYPES.APIKEY) {
        if (isEditMode) {
            schemas[`${fieldPrefix}ApikeyValue`] = createSecretValidation("API Key", VALIDATION_LIMITS.TOKEN_MIN, VALIDATION_LIMITS.TOKEN_MAX);
        } else {
            schemas[`${fieldPrefix}ApikeyValue`] = z.string().min(VALIDATION_LIMITS.TOKEN_MIN, `API Key must be at least ${VALIDATION_LIMITS.TOKEN_MIN} characters`).max(VALIDATION_LIMITS.TOKEN_MAX, "API Key too long");
        }
    }

    if (authType === AUTH_TYPES.OAUTH2) {
        schemas[`${fieldPrefix}Oauth2TokenUrl`] = z
            .string()
            .min(1, "Token URL is required")
            .url("Must be a valid HTTPS URL")
            .refine(url => url.startsWith("https://"), {
                message: "Token URL must use HTTPS for security",
            });
        schemas[`${fieldPrefix}Oauth2ClientId`] = z.string().min(1, "Client ID is required").max(VALIDATION_LIMITS.CLIENT_ID_MAX, "Client ID too long");

        if (isEditMode) {
            schemas[`${fieldPrefix}Oauth2ClientSecret`] = createSecretValidation("Client Secret", 1, VALIDATION_LIMITS.CLIENT_SECRET_MAX);
        } else {
            schemas[`${fieldPrefix}Oauth2ClientSecret`] = z.string().min(1, "Client Secret is required").max(VALIDATION_LIMITS.CLIENT_SECRET_MAX, "Client Secret too long");
        }

        schemas[`${fieldPrefix}Oauth2Scope`] = z.string().optional().or(z.literal(""));
    }

    return schemas;
};

interface AuthFieldsProps {
    control: any;
    register: any;
    errors: any;
    fieldPrefix?: string;
    trigger?: any;
    storedSecrets?: AuthStoredSecrets;
    authScope?: "agentCardAuth" | "taskAuth";
}

export const StaticBearerAuthFields = ({ control, errors, fieldPrefix = "auth", storedSecrets, authScope = "agentCardAuth" }: AuthFieldsProps) => {
    const fieldName = `${fieldPrefix}BearerToken`;
    const hasStoredValue = storedSecrets && authScope ? hasStoredAuthSecret(storedSecrets, authScope, "token") : false;

    return (
        <FormField label="Bearer Token" required={true} error={errors[fieldName]} helpText="Sent as: Authorization: Bearer <token>. Leave unchanged to keep current value when editing.">
            <PasswordInput name={fieldName} control={control} hasStoredValue={hasStoredValue} rules={{ required: true }} />
        </FormField>
    );
};

export const StaticApiKeyAuthFields = ({ control, errors, fieldPrefix = "auth", storedSecrets, authScope = "agentCardAuth" }: AuthFieldsProps) => {
    const fieldName = `${fieldPrefix}ApikeyValue`;
    const hasStoredValue = storedSecrets && authScope ? hasStoredAuthSecret(storedSecrets, authScope, "apiKey") : false;

    return (
        <FormField label="API Key Value" required={true} error={errors[fieldName]} helpText="Sent as: X-API-Key: <value> (header name is fixed). Leave unchanged to keep current value when editing.">
            <PasswordInput name={fieldName} control={control} hasStoredValue={hasStoredValue} rules={{ required: true }} />
        </FormField>
    );
};

export const OAuth2AuthFields = ({ control, register, errors, fieldPrefix = "auth", trigger, storedSecrets, authScope = "agentCardAuth" }: AuthFieldsProps) => {
    const tokenUrlField = `${fieldPrefix}Oauth2TokenUrl`;
    const clientIdField = `${fieldPrefix}Oauth2ClientId`;
    const clientSecretField = `${fieldPrefix}Oauth2ClientSecret`;
    const scopeField = `${fieldPrefix}Oauth2Scope`;

    const hasStoredValue = storedSecrets && authScope ? hasStoredAuthSecret(storedSecrets, authScope, "clientSecret") : false;

    const handleTokenUrlBlur = () => {
        if (trigger) {
            trigger(tokenUrlField);
        }
    };

    return (
        <div className="space-y-4">
            <TextInput name={tokenUrlField} label="Token URL" register={register} error={errors[tokenUrlField]} required helpText="OAuth 2.0 token endpoint (must use HTTPS)" onBlur={handleTokenUrlBlur} />
            <TextInput name={clientIdField} label="Client ID" register={register} error={errors[clientIdField]} required />
            <FormField label="Client Secret" required={true} error={errors[clientSecretField]} helpText="Leave unchanged to keep current value when editing.">
                <PasswordInput name={clientSecretField} control={control} hasStoredValue={hasStoredValue} rules={{ required: true }} />
            </FormField>
            <TextInput name={scopeField} label="Scope" register={register} error={errors[scopeField]} helpText="Optional: Space-separated OAuth 2.0 scopes" />
        </div>
    );
};
