// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { GatewayFieldSchema, GatewaySchema } from "@/types/models";

import { createFullGatewaySchemaValidator } from "./useGatewaySchemas";

const buildSchema = (fields: Record<string, GatewayFieldSchema>, isEditMode: boolean = false) => {
    const schema: GatewaySchema = {
        type: "test",
        name: "Test Gateway",
        description: "Test schema for the validator",
        schema: fields,
        yamlTemplatePath: "",
        requiredEnvVars: [],
    };
    return createFullGatewaySchemaValidator(schema, isEditMode);
};

const fullEnvelope = (values: Record<string, unknown>) => ({
    name: "valid-gateway-name",
    description: "Long enough description for the validator envelope.",
    values,
});

const expectValid = (validator: ReturnType<typeof buildSchema>, body: ReturnType<typeof fullEnvelope>) => {
    const result = validator.safeParse(body);
    if (!result.success) {
        throw new Error(`expected valid, got: ${JSON.stringify(result.error.issues)}`);
    }
};

const expectInvalidWithIssue = (validator: ReturnType<typeof buildSchema>, body: ReturnType<typeof fullEnvelope>, fieldName: string, messagePart: string) => {
    const result = validator.safeParse(body);
    expect(result.success).toBe(false);
    if (!result.success) {
        const matching = result.error.issues.find(i => i.path.join(".").endsWith(fieldName));
        expect(matching, `expected an issue on ${fieldName}; got ${JSON.stringify(result.error.issues)}`).toBeDefined();
        expect(matching!.message).toContain(messagePart);
    }
};

describe("createFullGatewaySchemaValidator — required vs conditional fields", () => {
    test("required field with minLength rejects empty value", () => {
        const validator = buildSchema({
            mailbox: { label: "Mailbox", type: "string", required: true, validation: { minLength: 1 } },
        });
        expectInvalidWithIssue(validator, fullEnvelope({ mailbox: "" }), "mailbox", "is required");
    });

    test("required field with minLength accepts a non-empty value", () => {
        const validator = buildSchema({
            mailbox: { label: "Mailbox", type: "string", required: true, validation: { minLength: 1 } },
        });
        expectValid(validator, fullEnvelope({ mailbox: "user@example.com" }));
    });

    test("optional field with no conditional accepts missing value", () => {
        const validator = buildSchema({
            description: { label: "Description", type: "string", required: false },
        });
        expectValid(validator, fullEnvelope({}));
    });

    test("conditional + required: required only when the condition matches", () => {
        const fields: Record<string, GatewayFieldSchema> = {
            auth_mode: { label: "Auth", type: "string", required: true },
            oauth2_tenant_id: {
                label: "Tenant",
                type: "string",
                required: true,
                conditionalDisplay: { auth_mode: "oauth2" },
            },
        };
        const validator = buildSchema(fields);

        expectInvalidWithIssue(validator, fullEnvelope({ auth_mode: "oauth2" }), "oauth2_tenant_id", "Tenant is required");

        expectValid(validator, fullEnvelope({ auth_mode: "basic" }));

        expectValid(validator, fullEnvelope({ auth_mode: "oauth2", oauth2_tenant_id: "tid" }));
    });

    test("conditional + NOT required: optional even when the condition matches", () => {
        const fields: Record<string, GatewayFieldSchema> = {
            auth_mode: { label: "Auth", type: "string", required: true },
            trusted_internal_tenant_id: {
                label: "Internal-trust tenant",
                type: "string",
                required: false,
                conditionalDisplay: { auth_mode: "oauth2" },
            },
        };
        const validator = buildSchema(fields);

        expectValid(validator, fullEnvelope({ auth_mode: "oauth2" }));
        expectValid(validator, fullEnvelope({ auth_mode: "basic" }));
        expectValid(validator, fullEnvelope({ auth_mode: "oauth2", trusted_internal_tenant_id: "tid" }));
    });

    test("conditional field with a pattern: stored empty string is accepted when the condition does not match", () => {
        const fields: Record<string, GatewayFieldSchema> = {
            connection_mode: { label: "Connection", type: "string", required: true },
            endpoint_url: {
                label: "Endpoint URL",
                type: "string",
                required: true,
                conditionalDisplay: { connection_mode: "custom" },
                validation: { pattern: "^(tcp|tcps|ws|wss)://[^\\s]+$" },
            },
        };
        const validator = buildSchema(fields, true);

        expectValid(validator, fullEnvelope({ connection_mode: "default", endpoint_url: "" }));
        expectInvalidWithIssue(validator, fullEnvelope({ connection_mode: "custom", endpoint_url: "" }), "endpoint_url", "Endpoint URL is required");
        expectInvalidWithIssue(validator, fullEnvelope({ connection_mode: "custom", endpoint_url: "not-a-url" }), "endpoint_url", "format is invalid");
        expectValid(validator, fullEnvelope({ connection_mode: "custom", endpoint_url: "tcp://broker:55555" }));
    });

    test("required string field with no minLength surfaces a friendly required message", () => {
        const validator = buildSchema({
            broker_password: { label: "Client Password", type: "password", required: true },
        });
        expectInvalidWithIssue(validator, fullEnvelope({ broker_password: "" }), "broker_password", "Client Password is required");
        expectValid(validator, fullEnvelope({ broker_password: "a-secret" }));
    });

    test("number field with min/max constraints rejects out-of-range values", () => {
        const validator = buildSchema({
            poll: { label: "Poll", type: "number", required: true, validation: { min: 1, max: 60 } },
        });

        expectInvalidWithIssue(validator, fullEnvelope({ poll: 0 }), "poll", "at least 1");
        expectInvalidWithIssue(validator, fullEnvelope({ poll: 100 }), "poll", "exceed 60");
        expectValid(validator, fullEnvelope({ poll: 30 }));
    });

    test("string field with pattern rejects invalid format using the schema's errorMsg", () => {
        const validator = buildSchema({
            email: {
                label: "Email",
                type: "string",
                required: true,
                validation: { pattern: "^[^@]+@[^@]+$", errorMsg: "Must be a valid email" },
            },
        });

        expectInvalidWithIssue(validator, fullEnvelope({ email: "not-an-email" }), "email", "Must be a valid email");
        expectValid(validator, fullEnvelope({ email: "user@example.com" }));
    });

    test("required error uses the schema's requiredMsg (minLength and baseline paths)", () => {
        const validator = buildSchema({
            access_token: {
                label: "Access Token",
                type: "password",
                required: true,
                validation: { minLength: 1, requiredMsg: "Access Token is required. Generate a permanent token." },
            },
            phone_number_id: {
                label: "Phone Number ID",
                type: "string",
                required: true,
                validation: { requiredMsg: "Phone Number ID is required. Find it in WhatsApp Manager." },
            },
        });

        expectInvalidWithIssue(validator, fullEnvelope({ access_token: "", phone_number_id: "1" }), "access_token", "Generate a permanent token");
        expectInvalidWithIssue(validator, fullEnvelope({ access_token: "x", phone_number_id: "" }), "phone_number_id", "Find it in WhatsApp Manager");
        expectValid(validator, fullEnvelope({ access_token: "x", phone_number_id: "1" }));
    });

    test("optional string field with pattern uses the schema's errorMsg", () => {
        const validator = buildSchema({
            phone_number_id: {
                label: "Phone Number ID",
                type: "string",
                required: false,
                validation: { pattern: "^[0-9]+$", errorMsg: "Must be digits only" },
            },
        });

        expectInvalidWithIssue(validator, fullEnvelope({ phone_number_id: "+1 555 0100" }), "phone_number_id", "Must be digits only");
        expectValid(validator, fullEnvelope({ phone_number_id: "1234567890" }));
        expectValid(validator, fullEnvelope({ phone_number_id: "" }));
    });

    test("edit mode + secret field: empty value preserves stored secret without minLength failure", () => {
        const fields: Record<string, GatewayFieldSchema> = {
            secret: {
                label: "Secret",
                type: "password",
                required: true,
                secret: true,
                validation: { minLength: 8 },
            },
        };
        const editValidator = buildSchema(fields, true);
        expectValid(editValidator, fullEnvelope({ secret: "" }));
        expectInvalidWithIssue(editValidator, fullEnvelope({ secret: "short" }), "secret", "at least 8 characters");
        expectValid(editValidator, fullEnvelope({ secret: "a-long-enough-secret" }));
    });

    test("create mode: empty required string fails minLength even when secret", () => {
        const fields: Record<string, GatewayFieldSchema> = {
            secret: {
                label: "Secret",
                type: "password",
                required: true,
                secret: true,
                validation: { minLength: 8 },
            },
        };
        const createValidator = buildSchema(fields, false);
        expectInvalidWithIssue(createValidator, fullEnvelope({ secret: "" }), "secret", "at least 8 characters");
    });

    test("required string with no minLength: rejects '' in create mode", () => {
        const validator = buildSchema({
            phone: { label: "Phone", type: "string", required: true },
        });
        expectInvalidWithIssue(validator, fullEnvelope({ phone: "" }), "phone", "is required");
        expectValid(validator, fullEnvelope({ phone: "555-1234" }));
    });

    test("required string with no minLength: rejects '' in edit mode for non-secret field", () => {
        const validator = buildSchema(
            {
                phone: { label: "Phone", type: "string", required: true },
            },
            true
        );
        expectInvalidWithIssue(validator, fullEnvelope({ phone: "" }), "phone", "is required");
        expectValid(validator, fullEnvelope({ phone: "555-1234" }));
    });

    test("required secret string with no minLength: allows '' in edit mode (keep-existing)", () => {
        const validator = buildSchema(
            {
                api_key: { label: "API Key", type: "password", required: true, secret: true },
            },
            true
        );
        expectValid(validator, fullEnvelope({ api_key: "" }));
        expectValid(validator, fullEnvelope({ api_key: "actual-key" }));
    });

    test("required secret string with no minLength: rejects '' in create mode", () => {
        const validator = buildSchema(
            {
                api_key: { label: "API Key", type: "password", required: true, secret: true },
            },
            false
        );
        expectInvalidWithIssue(validator, fullEnvelope({ api_key: "" }), "api_key", "is required");
    });

    test("optional string with pattern: allows '' without a format error", () => {
        const validator = buildSchema({
            tenant_id: {
                label: "Tenant ID",
                type: "string",
                required: false,
                validation: { pattern: "^[0-9a-f-]{36}$" },
            },
        });
        expectValid(validator, fullEnvelope({ tenant_id: "" }));
        expectValid(validator, fullEnvelope({ tenant_id: "12345678-1234-1234-1234-123456789abc" }));
        expectInvalidWithIssue(validator, fullEnvelope({ tenant_id: "not-a-guid" }), "tenant_id", "format is invalid");
    });

    test("required string with pattern: rejects '' and rejects malformed values", () => {
        const validator = buildSchema({
            broker_url: {
                label: "Host",
                type: "string",
                required: true,
                validation: { pattern: "^tcps?://.+$" },
            },
        });
        expectInvalidWithIssue(validator, fullEnvelope({ broker_url: "" }), "broker_url", "is required");
        expectInvalidWithIssue(validator, fullEnvelope({ broker_url: "http://bad" }), "broker_url", "format is invalid");
        expectValid(validator, fullEnvelope({ broker_url: "tcps://broker.example.com:55443" }));
    });

    test("envelope rejects too-short name and description", () => {
        const validator = buildSchema({});
        const result = validator.safeParse({
            name: "ab",
            description: "short",
            values: {},
        });
        expect(result.success).toBe(false);
        if (!result.success) {
            const paths = result.error.issues.map(i => i.path.join("."));
            expect(paths).toContain("name");
            expect(paths).toContain("description");
        }
    });
});

describe("createFullGatewaySchemaValidator — MCP gateway slug", () => {
    const buildMcpSchema = () =>
        createFullGatewaySchemaValidator({
            type: "mcp",
            name: "MCP Gateway",
            description: "MCP schema",
            schema: {},
            yamlTemplatePath: "",
            requiredEnvVars: [],
        });

    const envelope = (extra: Record<string, unknown>) => ({
        name: "valid-gateway-name",
        description: "Long enough description for the validator envelope.",
        values: {},
        ...extra,
    });

    test("accepts a valid slug", () => {
        const result = buildMcpSchema().safeParse(envelope({ slug: "my-mcp-gw" }));
        expect(result.success).toBe(true);
    });

    test("accepts an omitted or empty slug (optional)", () => {
        expect(buildMcpSchema().safeParse(envelope({})).success).toBe(true);
        expect(buildMcpSchema().safeParse(envelope({ slug: "" })).success).toBe(true);
    });

    test.each([
        ["too short", "ab"],
        ["uppercase", "MyGateway"],
        ["underscore", "my_gateway"],
        ["leading dash", "-mygw"],
        ["trailing dash", "mygw-"],
        ["leading digit", "1mygw"],
        ["reserved word", "oauth"],
        ["reserved eval reply gateway id", "evals"],
    ])("rejects an invalid slug (%s)", (_label, slug) => {
        const result = buildMcpSchema().safeParse(envelope({ slug }));
        expect(result.success).toBe(false);
        if (!result.success) {
            expect(result.error.issues.some(i => i.path.includes("slug"))).toBe(true);
        }
    });

    test("Google Chat entrypoints keep and validate the slug", () => {
        const googleChat = createFullGatewaySchemaValidator({
            type: "google_chat",
            name: "Google Chat",
            description: "Google Chat schema",
            schema: {},
            yamlTemplatePath: "",
            requiredEnvVars: [],
        });
        const valid = googleChat.safeParse(envelope({ slug: "hr-assistant" }));
        expect(valid.success && valid.data.slug).toBe("hr-assistant");
        expect(googleChat.safeParse(envelope({ slug: "Bad_Slug" })).success).toBe(false);
    });

    test("types without a custom endpoint do not validate a slug field", () => {
        const result = buildSchema({}).safeParse(envelope({ slug: "Bad_Slug!" }));
        expect(result.success).toBe(true);
    });
});

describe("createFullGatewaySchemaValidator — array<object> list fields", () => {
    const listField: GatewayFieldSchema = {
        label: "Include tools",
        type: "array<object>",
        itemKey: "pattern",
        itemLabel: "Pattern",
        itemValidation: { minLength: 1, maxLength: 255 },
    };

    test("accepts an empty list (the original unsaveable-form bug)", () => {
        const validator = buildSchema({ includeTools: listField });
        expectValid(validator, fullEnvelope({ includeTools: [] }));
    });

    test("accepts a populated list of single-key objects", () => {
        const validator = buildSchema({ includeTools: listField });
        expectValid(validator, fullEnvelope({ includeTools: [{ pattern: "foo" }, { pattern: "bar" }] }));
    });

    test("rejects a bare string instead of an array", () => {
        const validator = buildSchema({ includeTools: listField });
        const result = validator.safeParse(fullEnvelope({ includeTools: "foo" }));
        expect(result.success).toBe(false);
    });

    test("rejects an item whose value is empty", () => {
        const validator = buildSchema({ includeTools: listField });
        expectInvalidWithIssue(validator, fullEnvelope({ includeTools: [{ pattern: "" }] }), "pattern", "is required");
    });

    test("honors minItems / maxItems when declared", () => {
        const validator = buildSchema({
            includeTools: { ...listField, validation: { minItems: 1, maxItems: 2 } },
        });
        expectInvalidWithIssue(validator, fullEnvelope({ includeTools: [] }), "includeTools", "at least 1");
        expectInvalidWithIssue(validator, fullEnvelope({ includeTools: [{ pattern: "a" }, { pattern: "b" }, { pattern: "c" }] }), "includeTools", "not exceed 2");
        expectValid(validator, fullEnvelope({ includeTools: [{ pattern: "a" }] }));
    });

    test("falls back to a default item key when itemKey is absent", () => {
        const validator = buildSchema({
            origins: { label: "Origins", type: "array<object>" },
        });
        expectValid(validator, fullEnvelope({ origins: [{ value: "https://example.com" }] }));
    });
});

describe("createFullGatewaySchemaValidator — event_rules preserve authored fields", () => {
    const rulesField: GatewayFieldSchema = { label: "Event Rules", type: "event_rules" };
    const validRule = {
        name: "rule1",
        subscriptions: [{ topic: "orders/created" }],
        messageFormat: "json",
        targetAgent: "Orchestrator",
        promptTemplate: "Handle the order",
        successOutput: { enabled: false, topic: "" },
        errorOutput: { enabled: false, topic: "" },
    };

    test("retains run_as and the sibling identity/payload fields through the submit parse", () => {
        const validator = buildSchema({ rules: rulesField });
        const body = fullEnvelope({
            rules: [
                {
                    ...validRule,
                    runAs: "helpdesk-orchestrator",
                    inputExpression: "input.payload:body",
                    payloadEncoding: "utf-8",
                    artifactProcessing: { mode: "inline" },
                },
            ],
        });

        const result = validator.safeParse(body);
        expect(result.success).toBe(true);
        if (result.success) {
            const values = (result.data as { values: { rules: Record<string, unknown>[] } }).values;
            const rule = values.rules[0];
            expect(rule.runAs).toBe("helpdesk-orchestrator");
            expect(rule.inputExpression).toBe("input.payload:body");
            expect(rule.payloadEncoding).toBe("utf-8");
            expect(rule.artifactProcessing).toEqual({ mode: "inline" });
        }
    });

    test("retains a rule's eventFilter through the submit parse", () => {
        const eventFilter = {
            function: "equal",
            params: { positional: ["evaluate_expression(input.payload:type)", "order.created"] },
        };
        const validator = buildSchema({ rules: rulesField });

        const result = validator.safeParse(fullEnvelope({ rules: [{ ...validRule, eventFilter }, validRule] }));

        expect(result.success).toBe(true);
        if (result.success) {
            const values = (result.data as { values: { rules: Record<string, unknown>[] } }).values;
            expect(values.rules[0].eventFilter).toEqual(eventFilter);
            expect(values.rules[1]).not.toHaveProperty("eventFilter");
        }
    });
});
