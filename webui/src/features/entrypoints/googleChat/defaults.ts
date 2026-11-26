// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Gateway, GatewaySchema } from "@/types";

import { type GoogleChatFormInput, REDACTED } from "./schemas";

const DEFAULT_SLUG = "google-chat";

const BUILTIN_ORCHESTRATOR_NAME = "Orchestrator";

const text = (value: unknown): string => (typeof value === "string" ? value : "");

const bool = (value: unknown): boolean => value === true || value === "true";

const count = (value: unknown, fallback: number): number => {
    if (value === undefined || value === null || value === "") return fallback;
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : fallback;
};

const domains = (value: unknown): string[] => {
    if (Array.isArray(value)) return value.filter((entry): entry is string => typeof entry === "string");
    return text(value)
        .split(",")
        .map(entry => entry.trim())
        .filter(Boolean);
};

const schemaDefault = (schemaInfo: GatewaySchema, field: string): unknown => schemaInfo.schema?.[field]?.default;

export const hasStoredKey = (gateway: Gateway | undefined): boolean => gateway?.values?.service_account_key === REDACTED;

export function googleChatFormDefaults(schemaInfo: GatewaySchema, gateway: Gateway | undefined, isNew: boolean): GoogleChatFormInput {
    const stored = (isNew ? undefined : gateway?.values) ?? {};
    const pick = (field: string): unknown => (isNew ? schemaDefault(schemaInfo, field) : stored[field]);
    return {
        name: text(gateway?.name),
        description: text(gateway?.description),
        slug: isNew ? DEFAULT_SLUG : text(gateway?.slug),
        values: {
            addon_service_account_email: text(stored.addon_service_account_email),
            service_account_key: stored.service_account_key === REDACTED ? "" : text(stored.service_account_key),
            allowed_email_domains: domains(stored.allowed_email_domains),
            use_user_identity_in_channels: bool(pick("use_user_identity_in_channels")),
            default_agent_name: isNew ? text(schemaDefault(schemaInfo, "default_agent_name")) || BUILTIN_ORCHESTRATOR_NAME : text(stored.default_agent_name),
            thread_history_limit: count(pick("thread_history_limit"), 0),
            artifact_drive_folder_id: text(stored.artifact_drive_folder_id),
        },
    };
}
