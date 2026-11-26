// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { Gateway, GatewaySchema } from "@/types";

import { googleChatFormDefaults } from "./defaults";

const schemaInfo: GatewaySchema = {
    type: "google_chat",
    name: "Google Chat",
    description: "",
    schema: {
        use_user_identity_in_channels: { type: "boolean", default: true },
        thread_history_limit: { type: "number", default: 10 },
        default_agent_name: { type: "agent_select", default: "SchemaAgent" },
    },
    yamlTemplatePath: "",
    requiredEnvVars: [],
} as unknown as GatewaySchema;

const stored: Gateway = {
    id: "gw-1",
    slug: "hr-assistant",
    name: "HR Assistant",
    description: "Answers HR questions in Google Chat.",
    type: "google_chat",
    values: {
        addon_service_account_email: "service-1@gcp-sa-gsuiteaddons.iam.gserviceaccount.com",
        service_account_key: "<REDACTED>",
        allowed_email_domains: "example.com, example.org",
        use_user_identity_in_channels: "true",
        default_agent_name: "orchestrator-broker",
        thread_history_limit: "25",
        artifact_drive_folder_id: "0AB1cD2eF3gH4iJ5kL",
    },
};

describe("googleChatFormDefaults", () => {
    test("an edit starts from the stored values, with the saved key left blank", () => {
        expect(googleChatFormDefaults(schemaInfo, stored, false)).toStrictEqual({
            name: "HR Assistant",
            description: "Answers HR questions in Google Chat.",
            slug: "hr-assistant",
            values: {
                addon_service_account_email: "service-1@gcp-sa-gsuiteaddons.iam.gserviceaccount.com",
                service_account_key: "",
                allowed_email_domains: ["example.com", "example.org"],
                use_user_identity_in_channels: true,
                default_agent_name: "orchestrator-broker",
                thread_history_limit: 25,
                artifact_drive_folder_id: "0AB1cD2eF3gH4iJ5kL",
            },
        });
    });

    test("an edit keeps an allowed domain list stored as an array", () => {
        const values = googleChatFormDefaults(schemaInfo, { ...stored, values: { ...stored.values, allowed_email_domains: ["example.com", 7] } }, false).values;
        expect(values.allowed_email_domains).toStrictEqual(["example.com"]);
    });

    test("create starts from the schema defaults and the google-chat slug", () => {
        expect(googleChatFormDefaults(schemaInfo, undefined, true)).toStrictEqual({
            name: "",
            description: "",
            slug: "google-chat",
            values: {
                addon_service_account_email: "",
                service_account_key: "",
                allowed_email_domains: [],
                use_user_identity_in_channels: true,
                default_agent_name: "SchemaAgent",
                thread_history_limit: 10,
                artifact_drive_folder_id: "",
            },
        });
    });
});
