// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { ConnectorSubtypeSchema } from "@/types";

import { createConnectorConfigSchema } from "./schemaGenerator";

const createPortFieldSchema = (overrides = {}): ConnectorSubtypeSchema => ({
    subtype: "postgres",
    name: "Test Connector",
    description: "Test connector for unit tests",
    fieldSchema: {
        port: {
            type: "number",
            label: "Port",
            required: false,
            validation: { min: 1, max: 65535 },
            ...overrides,
        },
    },
});

const createStringFieldSchema = (fieldOverrides: Record<string, any> = {}): ConnectorSubtypeSchema => ({
    subtype: "openapi",
    name: "API Connector",
    description: "Test connector for string field tests",
    fieldSchema: {
        base_url: {
            type: "string",
            label: "Base URL",
            required: true,
            ...fieldOverrides,
        },
    },
});

describe("createConnectorConfigSchema", () => {
    describe("optional number field validation", () => {
        test("accepts undefined for optional number field", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: undefined });

            expect(result.success).toBe(true);
        });

        test("accepts null for optional number field", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: null });

            expect(result.success).toBe(true);
        });

        test("accepts empty string for optional number field", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: "" });

            expect(result.success).toBe(true);
        });

        test("accepts NaN for optional number field (converts to undefined)", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: NaN });

            expect(result.success).toBe(true);
        });

        test("accepts valid number within range", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: 5432 });

            expect(result.success).toBe(true);
            if (result.success) {
                expect(result.data.port).toBe(5432);
            }
        });

        test("rejects number below minimum with error message", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: 0 });

            expect(result.success).toBe(false);
            if (!result.success) {
                expect(result.error.issues[0].message).toBe("Minimum value is 1");
            }
        });

        test("rejects number above maximum with error message", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: 99999 });

            expect(result.success).toBe(false);
            if (!result.success) {
                expect(result.error.issues[0].message).toBe("Maximum value is 65535");
            }
        });

        test("accepts boundary values (min)", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: 1 });

            expect(result.success).toBe(true);
        });

        test("accepts boundary values (max)", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: 65535 });

            expect(result.success).toBe(true);
        });

        test("converts string numbers to numbers", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());
            const result = schema.safeParse({ port: "5432" });

            expect(result.success).toBe(true);
            if (result.success) {
                expect(result.data.port).toBe(5432);
            }
        });

        test("rejects invalid string with thrown error", () => {
            const schema = createConnectorConfigSchema(createPortFieldSchema());

            expect(() => schema.parse({ port: "abc" })).toThrow("Port must be a number");
        });
    });

    describe("required string field with no explicit minLength", () => {
        test("rejects empty string in create mode", () => {
            const schema = createConnectorConfigSchema(createStringFieldSchema(), {}, false);
            const result = schema.safeParse({ base_url: "" });

            expect(result.success).toBe(false);
            if (!result.success) {
                const issue = result.error.issues.find(i => i.path.includes("base_url"));
                expect(issue).toBeDefined();
                expect(issue!.message).toContain("is required");
            }
        });

        test("rejects empty string in edit mode for non-secret field", () => {
            const schema = createConnectorConfigSchema(createStringFieldSchema(), {}, true);
            const result = schema.safeParse({ base_url: "" });

            expect(result.success).toBe(false);
            if (!result.success) {
                const issue = result.error.issues.find(i => i.path.includes("base_url"));
                expect(issue).toBeDefined();
                expect(issue!.message).toContain("is required");
            }
        });

        test("accepts non-empty string in both modes", () => {
            const createSchema = createConnectorConfigSchema(createStringFieldSchema(), {}, false);
            const editSchema = createConnectorConfigSchema(createStringFieldSchema(), {}, true);

            expect(createSchema.safeParse({ base_url: "https://api.example.com" }).success).toBe(true);
            expect(editSchema.safeParse({ base_url: "https://api.example.com" }).success).toBe(true);
        });

        test("allows empty string in edit mode for secret field", () => {
            const schema = createConnectorConfigSchema(createStringFieldSchema({ secret: true, type: "password" }), {}, true);
            const result = schema.safeParse({ base_url: "" });

            expect(result.success).toBe(true);
        });

        test("rejects empty string in create mode for secret field", () => {
            const schema = createConnectorConfigSchema(createStringFieldSchema({ secret: true, type: "password" }), {}, false);
            const result = schema.safeParse({ base_url: "" });

            expect(result.success).toBe(false);
            if (!result.success) {
                const issue = result.error.issues.find(i => i.path.includes("base_url"));
                expect(issue).toBeDefined();
                expect(issue!.message).toContain("is required");
            }
        });
    });

    describe("hidden fields", () => {
        const createHiddenFieldSchema = (fieldOverrides: Record<string, any> = {}): ConnectorSubtypeSchema => ({
            subtype: "remote",
            name: "MCP Connector",
            description: "Test connector for hidden field tests",
            fieldSchema: {
                server_url: {
                    type: "string",
                    label: "Server URL",
                    required: true,
                },
                tool_name: {
                    type: "string",
                    label: "Single Tool Name",
                    required: false,
                    hidden: true,
                    ...fieldOverrides,
                },
            },
        });

        test("hidden field: accepts any value without validation", () => {
            const schema = createConnectorConfigSchema(createHiddenFieldSchema());
            const result = schema.safeParse({ server_url: "https://example.com", tool_name: "my_tool" });

            expect(result.success).toBe(true);
        });

        test("hidden field: accepts undefined (omitted from form)", () => {
            const schema = createConnectorConfigSchema(createHiddenFieldSchema());
            const result = schema.safeParse({ server_url: "https://example.com" });

            expect(result.success).toBe(true);
        });

        test("hidden field: accepts null", () => {
            const schema = createConnectorConfigSchema(createHiddenFieldSchema());
            const result = schema.safeParse({ server_url: "https://example.com", tool_name: null });

            expect(result.success).toBe(true);
        });

        test("hidden required field: does not trigger required validation when absent", () => {
            const schema = createConnectorConfigSchema(createHiddenFieldSchema({ required: true }));
            const result = schema.safeParse({ server_url: "https://example.com" });

            expect(result.success).toBe(true);
        });

        test("hidden field: accepts arbitrary object (no type enforcement)", () => {
            const schema = createConnectorConfigSchema(createHiddenFieldSchema());
            const result = schema.safeParse({ server_url: "https://example.com", tool_name: { nested: "value" } });

            expect(result.success).toBe(true);
        });

        test("non-hidden required field still validates when hidden sibling is absent", () => {
            const schema = createConnectorConfigSchema(createHiddenFieldSchema());
            const result = schema.safeParse({ server_url: "" });

            expect(result.success).toBe(false);
            if (!result.success) {
                const issue = result.error.issues.find(i => i.path.includes("server_url"));
                expect(issue).toBeDefined();
            }
        });
    });

    describe("optional string field with pattern validation", () => {
        const createPatternFieldSchema = (required: boolean): ConnectorSubtypeSchema => ({
            subtype: "openapi",
            name: "Pattern Test Connector",
            description: "Test connector for pattern validation tests",
            fieldSchema: {
                tenant_id: {
                    type: "string",
                    label: "Tenant ID",
                    required,
                    validation: { pattern: "^[0-9a-f-]{36}$" },
                },
            },
        });

        test("optional field with pattern: allows empty string without format error", () => {
            const schema = createConnectorConfigSchema(createPatternFieldSchema(false));
            const result = schema.safeParse({ tenant_id: "" });

            expect(result.success).toBe(true);
        });

        test("optional field with pattern: accepts a well-formed value", () => {
            const schema = createConnectorConfigSchema(createPatternFieldSchema(false));
            const result = schema.safeParse({ tenant_id: "12345678-1234-1234-1234-123456789abc" });

            expect(result.success).toBe(true);
        });

        test("optional field with pattern: rejects a non-empty malformed value", () => {
            const schema = createConnectorConfigSchema(createPatternFieldSchema(false));
            const result = schema.safeParse({ tenant_id: "not-a-guid" });

            expect(result.success).toBe(false);
            if (!result.success) {
                const issue = result.error.issues.find(i => i.path.includes("tenant_id"));
                expect(issue).toBeDefined();
                expect(issue!.message).toContain("Invalid format");
            }
        });

        test("required field with pattern: rejects empty string", () => {
            const schema = createConnectorConfigSchema(createPatternFieldSchema(true));
            const result = schema.safeParse({ tenant_id: "" });

            expect(result.success).toBe(false);
        });
    });
});
