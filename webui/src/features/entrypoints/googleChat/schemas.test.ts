// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";
import type { z } from "zod";

import { withGeneratedKey } from "@/forms/testing/generatedKey";

import { type GoogleChatFormInput, type GoogleChatFormOutput, googleChatFormSchema, type GoogleChatSchemaContext, REDACTED, toGoogleChatValues } from "./schemas";

const ADDON = "service-123456789@gcp-sa-gsuiteaddons.iam.gserviceaccount.com";

const createCtx: GoogleChatSchemaContext = { isNew: true, hasStoredKey: false };
const editCtx: GoogleChatSchemaContext = { isNew: false, hasStoredKey: true };

const validKey = () => withGeneratedKey('{"type":"service_account","client_email":"bot@proj.iam.gserviceaccount.com","private_key":"{{PRIVATE_KEY_PEM}}"}');

function form(overrides: Partial<Omit<GoogleChatFormInput, "values">> & { values?: Partial<GoogleChatFormInput["values"]> } = {}): GoogleChatFormInput {
    const { values, ...rest } = overrides;
    return {
        name: "Google Chat",
        description: "Answers questions in Google Chat.",
        slug: "google-chat",
        ...rest,
        values: {
            addon_service_account_email: ADDON,
            service_account_key: REDACTED,
            allowed_email_domains: [],
            use_user_identity_in_channels: false,
            default_agent_name: "OrchestratorAgent",
            thread_history_limit: 0,
            artifact_drive_folder_id: "",
            ...values,
        },
    };
}

function messagesAt(result: z.ZodSafeParseResult<unknown>, path: string): string[] {
    if (result.success) return [];
    return result.error.issues.filter(issue => issue.path.join(".") === path).map(issue => issue.message);
}

const parse = (ctx: GoogleChatSchemaContext, input: GoogleChatFormInput) => googleChatFormSchema(ctx).safeParse(input);

describe("googleChatFormSchema", () => {
    test("a complete edit form with the stored key passes", () => {
        expect(parse(editCtx, form()).success).toBe(true);
    });

    test("own service account gets the specific message", () => {
        const result = parse(editCtx, form({ values: { addon_service_account_email: "temp@proj.iam.gserviceaccount.com" } }));
        expect(messagesAt(result, "values.addon_service_account_email")).toEqual(["That is the project's own service account. Use the add-on service account, service-<project number>@gcp-sa-gsuiteaddons.iam.gserviceaccount.com."]);
    });

    test("any other address gets the add-on pattern message", () => {
        const result = parse(editCtx, form({ values: { addon_service_account_email: "someone@example.net" } }));
        expect(messagesAt(result, "values.addon_service_account_email")).toEqual(["Enter the add-on's Google-managed service account, service-<project number>@gcp-sa-gsuiteaddons.iam.gserviceaccount.com"]);
    });

    test("create refuses <REDACTED> and an empty key, and accepts a real one", async () => {
        expect(messagesAt(parse(createCtx, form()), "values.service_account_key")).not.toEqual([]);
        expect(messagesAt(parse(createCtx, form({ values: { service_account_key: "" } })), "values.service_account_key")).not.toEqual([]);
        const result = parse(createCtx, form({ values: { service_account_key: await validKey() } }));
        expect(messagesAt(result, "values.service_account_key")).toEqual([]);
    });

    test("a replaced key on edit is checked with the fixed key messages", () => {
        const result = parse(editCtx, form({ values: { service_account_key: '{"type":"user"}' } }));
        expect(messagesAt(result, "values.service_account_key")).toEqual(["Service account key must be the JSON key file downloaded for a Google Cloud service account."]);
    });

    test("an empty key on edit keeps the saved one, but is required when none is saved", () => {
        const empty = form({ values: { service_account_key: "" } });
        expect(messagesAt(parse(editCtx, empty), "values.service_account_key")).toEqual([]);
        expect(messagesAt(parse({ ...editCtx, hasStoredKey: false }, empty), "values.service_account_key")).toEqual(["Upload the service account's JSON key file"]);
    });

    test("a slug or name another entrypoint already uses is refused before the request", async () => {
        const ctx = { ...createCtx, takenSlugs: new Set(["google-chat"]), takenNames: new Set(["Google Chat"]) };
        expect(messagesAt(parse(ctx, form()), "slug")).toEqual(["Another entrypoint already uses this URL endpoint. Choose a different one."]);
        expect(messagesAt(parse(ctx, form()), "name")).toEqual(["Another entrypoint already uses this name. Choose a different one."]);
        expect(parse(ctx, form({ name: "HR Bot", slug: "hr-bot", values: { service_account_key: await validKey() } })).success).toBe(true);
    });

    test("slug is required and checked on create only", () => {
        expect(messagesAt(parse(createCtx, form({ slug: "" })), "slug")).not.toEqual([]);
        expect(messagesAt(parse(createCtx, form({ slug: "api" })), "slug")).not.toEqual([]);
        expect(messagesAt(parse(createCtx, form({ slug: "Bad_Slug" })), "slug")).not.toEqual([]);
        expect(messagesAt(parse(editCtx, form({ slug: "" })), "slug")).toEqual([]);
    });

    test("thread history limit is a whole number from 0 to 100", () => {
        for (const ok of [0, 100]) expect(messagesAt(parse(editCtx, form({ values: { thread_history_limit: ok } })), "values.thread_history_limit")).toEqual([]);
        for (const bad of [-1, 101, 2.5]) expect(messagesAt(parse(editCtx, form({ values: { thread_history_limit: bad } })), "values.thread_history_limit")).not.toEqual([]);
    });

    test("default agent is required", () => {
        expect(messagesAt(parse(editCtx, form({ values: { default_agent_name: "" } })), "values.default_agent_name")).not.toEqual([]);
    });

    test("a pasted Drive link is reduced to its id; a bad id is refused", () => {
        const result = parse(editCtx, form({ values: { artifact_drive_folder_id: "https://drive.google.com/drive/folders/1AbCdEfGhIjK" } }));
        expect(result.success && result.data.values.artifact_drive_folder_id).toBe("1AbCdEfGhIjK");
        expect(messagesAt(parse(editCtx, form({ values: { artifact_drive_folder_id: "short" } })), "values.artifact_drive_folder_id")).not.toEqual([]);
    });

    test("invalid domain chips fail with the domain message", () => {
        const result = parse(editCtx, form({ values: { allowed_email_domains: ["gmail"] } }));
        expect(messagesAt(result, "values.allowed_email_domains")).toEqual(["Invalid entry. Enter only email domains."]);
    });

    test("description keeps today's limits", () => {
        expect(messagesAt(parse(editCtx, form({ description: "short" })), "description")).not.toEqual([]);
        expect(messagesAt(parse(editCtx, form({ description: "x".repeat(1001) })), "description")).not.toEqual([]);
    });

    test("stored assistant context the form does not render neither blocks the save nor reaches the payload", () => {
        const stored = { ...form(), values: { ...form().values, systemPurpose: "x".repeat(4001), responseFormat: "y" } };
        const result = parse(editCtx, stored);
        expect(result.success).toBe(true);
        expect(result.data?.values).not.toHaveProperty("systemPurpose");
        expect(result.data?.values).not.toHaveProperty("responseFormat");
    });

    test("the key error shows even while another field is invalid", () => {
        const emptiedNumber = undefined as unknown as number;
        const result = parse(createCtx, form({ name: "", values: { thread_history_limit: emptiedNumber } }));
        expect(messagesAt(result, "values.service_account_key")).not.toEqual([]);
    });
});

describe("toGoogleChatValues", () => {
    const output = (overrides: Parameters<typeof form>[0] = {}): GoogleChatFormOutput => form(overrides);

    test("create sends delivery https and every value", () => {
        const sent = toGoogleChatValues(output({ values: { service_account_key: "{}" } }), null);
        expect(sent.delivery).toBe("https");
        expect(sent).toMatchObject({ addon_service_account_email: ADDON, default_agent_name: "OrchestratorAgent", service_account_key: "{}", thread_history_limit: 0 });
    });

    test("on edit sends only dirty keys and never <REDACTED>", () => {
        const sent = toGoogleChatValues(output({ values: { default_agent_name: "Other" } }), { values: { default_agent_name: true, service_account_key: true } });
        expect(sent).toEqual({ default_agent_name: "Other" });
    });

    test("on edit an array field counts as dirty when any entry is", () => {
        const sent = toGoogleChatValues(output({ values: { allowed_email_domains: ["example.org"] } }), { values: { allowed_email_domains: [false, true] } });
        expect(sent).toEqual({ allowed_email_domains: ["example.org"] });
    });

    test("on edit an empty key marked dirty is not sent, so the stored key is kept", () => {
        const sent = toGoogleChatValues(output({ values: { service_account_key: "", default_agent_name: "Other" } }), { values: { service_account_key: true, default_agent_name: true } });
        expect(sent).toEqual({ default_agent_name: "Other" });
    });

    test("on edit a replaced key is sent", () => {
        const sent = toGoogleChatValues(output({ values: { service_account_key: "{}" } }), { values: { service_account_key: true } });
        expect(sent).toEqual({ service_account_key: "{}" });
    });
});
