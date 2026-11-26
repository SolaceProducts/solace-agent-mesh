// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

import { allowedDomainsSchema } from "./domains";
import { driveFolderIdFromInput, driveFolderProblem } from "./driveFolder";
import { serviceAccountKeyProblem } from "./serviceAccountKey";

export const REDACTED = "<REDACTED>";

const SLUG_RESERVED: ReadonlySet<string> = new Set(["oauth", "api", "well-known", "gw", "health", "evals"]);
const SLUG_PATTERN = /^[a-z][a-z0-9-]*[a-z0-9]$/;
const SLUG_MIN = 3;
const SLUG_MAX = 63;
const SLUG_TAKEN = "Another entrypoint already uses this URL endpoint. Choose a different one.";
const NAME_TAKEN = "Another entrypoint already uses this name. Choose a different one.";
const SLUG_MESSAGE = "URL endpoint must be 3-63 characters: lowercase letters, digits, and dashes; start with a letter and end with a letter or digit; and not be a reserved word (oauth, api, well-known, gw, health, evals).";

const THREAD_HISTORY_MAX = 100;

const ADDON_PATTERN = /^service-\d+@gcp-sa-gsuiteaddons\.iam\.gserviceaccount\.com$/;
const OWN_SERVICE_ACCOUNT = /^[^@]+@(?!gcp-sa-gsuiteaddons\.)[^@]+\.iam\.gserviceaccount\.com$/;

const MESSAGES = {
    addonRequired: "Enter the add-on's service account email",
    addonOwnAccount: "That is the project's own service account. Use the add-on service account, service-<project number>@gcp-sa-gsuiteaddons.iam.gserviceaccount.com.",
    addonPattern: "Enter the add-on's Google-managed service account, service-<project number>@gcp-sa-gsuiteaddons.iam.gserviceaccount.com",
    keyRequired: "Upload the service account's JSON key file",
    threadHistory: `Enter a whole number from 0 to ${THREAD_HISTORY_MAX}`,
    agentRequired: "Select the agent that answers",
} as const;

export const isValidSlug = (slug: string): boolean => slug.length >= SLUG_MIN && slug.length <= SLUG_MAX && SLUG_PATTERN.test(slug) && !SLUG_RESERVED.has(slug);

export interface GoogleChatSchemaContext {
    isNew: boolean;
    hasStoredKey: boolean;
    takenSlugs?: ReadonlySet<string>;
    takenNames?: ReadonlySet<string>;
}

function addonProblem(email: string): string | null {
    if (email === "") return MESSAGES.addonRequired;
    if (ADDON_PATTERN.test(email)) return null;
    return OWN_SERVICE_ACCOUNT.test(email) ? MESSAGES.addonOwnAccount : MESSAGES.addonPattern;
}

function keyProblem(key: string, ctx: GoogleChatSchemaContext): string | null {
    if ((key === "" || key === REDACTED) && !ctx.isNew && ctx.hasStoredKey) return null;
    if (key === "") return MESSAGES.keyRequired;
    return serviceAccountKeyProblem(key);
}

const valuesSchema = z.object({
    addon_service_account_email: z.string().superRefine((email, issue) => {
        const problem = addonProblem(email);
        if (problem) issue.addIssue({ code: "custom", message: problem });
    }),
    service_account_key: z.string(),
    allowed_email_domains: allowedDomainsSchema,
    use_user_identity_in_channels: z.boolean(),
    default_agent_name: z.string().min(1, MESSAGES.agentRequired),
    thread_history_limit: z.number({ message: MESSAGES.threadHistory }).int(MESSAGES.threadHistory).min(0, MESSAGES.threadHistory).max(THREAD_HISTORY_MAX, MESSAGES.threadHistory),
    artifact_drive_folder_id: z
        .string()
        .superRefine((raw, issue) => {
            const problem = driveFolderProblem(raw);
            if (problem) issue.addIssue({ code: "custom", message: problem });
        })
        .transform(driveFolderIdFromInput),
});

const NONE_TAKEN: ReadonlySet<string> = new Set();

const slugSchema = (isNew: boolean, taken: ReadonlySet<string>) =>
    isNew
        ? z
              .string()
              .refine(isValidSlug, SLUG_MESSAGE)
              .refine(slug => !taken.has(slug), SLUG_TAKEN)
        : z.string();

const baseSchema = (isNew: boolean, takenSlugs: ReadonlySet<string> = NONE_TAKEN, takenNames: ReadonlySet<string> = NONE_TAKEN) =>
    z.object({
        name: z
            .string()
            .min(3, "Name must be at least 3 characters")
            .max(255, "Name must not exceed 255 characters")
            .refine(name => !takenNames.has(name.trim()), NAME_TAKEN),
        description: z.string().min(10, "Description must be at least 10 characters").max(1000, "Description must not exceed 1,000 characters"),
        slug: slugSchema(isNew, takenSlugs),
        values: valuesSchema,
    });

type BaseSchema = ReturnType<typeof baseSchema>;
export type GoogleChatFormInput = z.input<BaseSchema>;
export type GoogleChatFormOutput = z.output<BaseSchema>;
export type GoogleChatValues = GoogleChatFormOutput["values"];

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value);
const text = (value: unknown) => (typeof value === "string" ? value : "");

export function googleChatFormSchema(ctx: GoogleChatSchemaContext): BaseSchema {
    return baseSchema(ctx.isNew, ctx.takenSlugs, ctx.takenNames).superRefine(
        (form, issue) => {
            const { values } = form;
            const keyMessage = keyProblem(text(values.service_account_key), ctx);
            if (keyMessage) issue.addIssue({ code: "custom", message: keyMessage, path: ["values", "service_account_key"] });
        },
        { when: payload => isRecord(payload.value) && isRecord(payload.value.values) }
    );
}

export interface GoogleChatDirty {
    values?: Partial<Record<keyof GoogleChatValues, unknown>>;
}

function isDirtyMark(mark: unknown): boolean {
    if (mark === true) return true;
    if (Array.isArray(mark)) return mark.some(isDirtyMark);
    return isRecord(mark) && Object.values(mark).some(isDirtyMark);
}

export type GoogleChatPayloadValues = Partial<GoogleChatValues> & { delivery?: "https"; endpoint_url?: string };

export function toGoogleChatValues(output: GoogleChatFormOutput, dirty: GoogleChatDirty | null): GoogleChatPayloadValues {
    const { values } = output;
    if (dirty === null) return { ...withoutRedactedKey(values), delivery: "https" };
    const entries = (Object.keys(values) as (keyof GoogleChatValues)[]).filter(key => isDirtyMark(dirty.values?.[key])).map(key => [key, values[key]] as const);
    return withoutKeptKey(Object.fromEntries(entries) as Partial<GoogleChatValues>);
}

function withoutRedactedKey<T extends Partial<GoogleChatValues>>(values: T): T {
    return values.service_account_key === REDACTED ? withoutKey(values) : values;
}

function withoutKeptKey<T extends Partial<GoogleChatValues>>(values: T): T {
    return values.service_account_key === REDACTED || values.service_account_key === "" ? withoutKey(values) : values;
}

function withoutKey<T extends Partial<GoogleChatValues>>(values: T): T {
    const { service_account_key: _redacted, ...rest } = values;
    return rest as T;
}
