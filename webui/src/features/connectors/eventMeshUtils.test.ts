// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import {
    createEventMeshConnectorSchema,
    eventMeshDuplicateToolNameUids,
    eventMeshOperationFieldError,
    eventMeshOperationIssues,
    eventMeshParametersFromSchema,
    eventMeshPlaceholders,
    eventMeshSchemaFromParameters,
    newEventMeshOperation,
    normalizeEventMeshValues,
    parseEventMeshInputSchemaJSON,
    responseModeValue,
    stripEventMeshUiFields,
} from "./eventMeshUtils";

function validOperation(overrides: Record<string, unknown> = {}) {
    return {
        tool_name: "get_order_status",
        tool_description: "Look up the current status of a customer order",
        topic: "acme/orders/v1/{order_id}/status",
        wait_for_response: true,
        request_expiry_ms: 60000,
        reply_mode: "temp_queue",
        qos: 1,
        payload_format: "json",
        input_schema: {
            type: "object",
            required: ["order_id"],
            properties: {
                order_id: { type: "string", description: "The customer order identifier" },
            },
        },
        ...overrides,
    };
}

function connector(values: Record<string, unknown>) {
    return {
        name: "test_connector",
        description: "A test event mesh connector",
        values,
    };
}

function brokerValues(overrides: Record<string, unknown> = {}) {
    return {
        broker_url: "tcps://broker.example.com:55443",
        broker_vpn: "default",
        broker_username: "agent",
        broker_password: "s3cret",
        operations: [validOperation()],
        ...overrides,
    };
}

describe("createEventMeshConnectorSchema — operations[] shape", () => {
    test("accepts a valid multi-operation connector", () => {
        const schema = createEventMeshConnectorSchema(false);
        const result = schema.safeParse(
            connector(
                brokerValues({
                    operations: [
                        validOperation(),
                        validOperation({
                            tool_name: "publish_inventory_adjustment",
                            wait_for_response: false,
                            topic: "acme/inventory/v1/{store_id}/adjustment",
                            input_schema: {
                                type: "object",
                                required: ["store_id"],
                                properties: { store_id: { type: "string", description: "Store id" } },
                            },
                        }),
                    ],
                })
            )
        );
        expect(result.success).toBe(true);
    });

    test("rejects an empty operations array", () => {
        const schema = createEventMeshConnectorSchema(false);
        const result = schema.safeParse(connector(brokerValues({ operations: [] })));
        expect(result.success).toBe(false);
    });

    test("rejects duplicate tool names", () => {
        const schema = createEventMeshConnectorSchema(false);
        const result = schema.safeParse(connector(brokerValues({ operations: [validOperation(), validOperation()] })));
        expect(result.success).toBe(false);
    });

    test("rejects a topic referencing an undeclared input property", () => {
        const schema = createEventMeshConnectorSchema(false);
        const result = schema.safeParse(
            connector(
                brokerValues({
                    operations: [validOperation({ topic: "acme/orders/v1/{missing_prop}/status" })],
                })
            )
        );
        expect(result.success).toBe(false);
    });

    test.each([
        ["legacy double brace", "acme/orders/v1/{{order_id}}/status"],
        ["legacy double brace spaced", "acme/orders/v1/{{ order_id }}/status"],
        ["legacy scheme embed", "acme/orders/v1/{{ text://order_id }}/status"],
    ])("accepts a %s topic", (_label, topic) => {
        const schema = createEventMeshConnectorSchema(false);
        const result = schema.safeParse(connector(brokerValues({ operations: [validOperation({ topic })] })));
        expect(result.success).toBe(true);
    });
});

describe("createEventMeshConnectorSchema — broker_url host URI format", () => {
    test.each(["tcp://broker.example.com:55555", "tcps://broker.example.com:55443", "tcps://192.168.0.1"])("accepts a valid SMF host URI %s", broker_url => {
        const schema = createEventMeshConnectorSchema(false);
        const result = schema.safeParse(connector(brokerValues({ broker_url })));
        expect(result.success).toBe(true);
    });

    test.each(["not-a-url", "https://broker.example.com", "broker.example.com:55443", "tcps://"])("rejects an invalid host URI %s", broker_url => {
        const schema = createEventMeshConnectorSchema(false);
        const result = schema.safeParse(connector(brokerValues({ broker_url })));
        expect(result.success).toBe(false);
        if (!result.success) {
            const hitUrl = result.error.issues.some(issue => issue.path.includes("broker_url"));
            expect(hitUrl).toBe(true);
        }
    });
});

describe("createEventMeshConnectorSchema — broker password new vs edit", () => {
    test("new connector REQUIRES a broker password", () => {
        const schema = createEventMeshConnectorSchema(false);
        const result = schema.safeParse(connector(brokerValues({ broker_password: "" })));
        expect(result.success).toBe(false);
        if (!result.success) {
            const hitPassword = result.error.issues.some(issue => issue.path.includes("broker_password"));
            expect(hitPassword).toBe(true);
        }
    });

    test("edit connector ALLOWS an empty broker password (keeps stored secret)", () => {
        const schema = createEventMeshConnectorSchema(true);
        const result = schema.safeParse(connector(brokerValues({ broker_password: "" })));
        expect(result.success).toBe(true);
    });
});

describe("parseEventMeshInputSchemaJSON", () => {
    test("defaults missing payload field paths to the property name", () => {
        const schema = parseEventMeshInputSchemaJSON(
            JSON.stringify({
                type: "object",
                required: ["action"],
                properties: {
                    action: { type: "string", enum: ["status", "add"] },
                    order_id: { type: "string", "x-solace-payload-path": "request.order_id" },
                },
            })
        );

        const parameters = eventMeshParametersFromSchema(schema);
        expect(parameters).toEqual(expect.arrayContaining([expect.objectContaining({ name: "action", payload_path: "action" }), expect.objectContaining({ name: "order_id", payload_path: "request.order_id" })]));
    });

    test("throws on invalid JSON", () => {
        expect(() => parseEventMeshInputSchemaJSON("{ not json")).toThrow();
    });

    test("throws when the schema root is not an object type", () => {
        expect(() => parseEventMeshInputSchemaJSON(JSON.stringify({ type: "array", properties: {} }))).toThrow();
    });
});

describe("responseModeValue", () => {
    test("maps wait_for_response to the select value", () => {
        expect(responseModeValue(true)).toBe("request_reply");
        expect(responseModeValue(false)).toBe("publish_subscribe");
        expect(responseModeValue(undefined)).toBe("publish_subscribe");
    });
});

describe("eventMeshSchemaFromParameters ↔ eventMeshParametersFromSchema round-trip", () => {
    test("preserves required, payload path, enum, and context expression", () => {
        const schema = eventMeshSchemaFromParameters([
            { name: "order_id", type: "string", required: true, description: "Order id", payload_path: "request.order_id" },
            { name: "region", type: "string", context_expression: "user.region", required: true },
            { name: "limit", type: "integer", enum: [10, 20], default: 10 },
        ]);

        const params = eventMeshParametersFromSchema(schema);
        const byName = Object.fromEntries(params.map(p => [p.name, p]));

        expect(byName.order_id).toMatchObject({ required: true, payload_path: "request.order_id" });
        expect(byName.region.context_expression).toBe("user.region");
        expect(byName.region.required).toBe(false);
        expect(byName.limit).toMatchObject({ type: "integer", enum: [10, 20] });
    });
});

describe("normalizeEventMeshValues", () => {
    test("seeds a single default operation when none are present", () => {
        const normalized = normalizeEventMeshValues({});
        expect(Array.isArray(normalized.operations)).toBe(true);
        expect(normalized.operations).toHaveLength(1);
    });

    test("fills operation defaults while preserving provided fields", () => {
        const normalized = normalizeEventMeshValues({ operations: [{ tool_name: "my_tool" }] });
        const op = normalized.operations[0];
        expect(op.tool_name).toBe("my_tool");
        expect(op.payload_format).toBe("json");
        expect(op.wait_for_response).toBe(true);
        expect(op.input_schema).toMatchObject({ type: "object" });
    });
});

describe("newEventMeshOperation", () => {
    test("opens with empty text fields so placeholders are not saved as data", () => {
        const op = newEventMeshOperation();
        expect(op.tool_name).toBe("");
        expect(op.tool_description).toBe("");
        expect(op.topic).toBe("");
    });
});

describe("createEventMeshConnectorSchema — step-scoped operations", () => {
    test("skips operation validation on the config step so an incomplete seed tool doesn't block Next", () => {
        const schema = createEventMeshConnectorSchema(false, false);
        const result = schema.safeParse(connector(brokerValues({ operations: [{ ...validOperation(), tool_name: "", tool_description: "", topic: "" }] })));
        expect(result.success).toBe(true);
    });

    test("still enforces operations when validation is on (tools/review/create)", () => {
        const schema = createEventMeshConnectorSchema(false, true);
        const result = schema.safeParse(connector(brokerValues({ operations: [{ ...validOperation(), tool_description: "" }] })));
        expect(result.success).toBe(false);
    });
});

describe("eventMeshOperationIssues", () => {
    test("returns no issues for a valid operation", () => {
        expect(eventMeshOperationIssues(validOperation() as never)).toEqual([]);
    });

    test("flags an empty dynamic level in the topic", () => {
        const issues = eventMeshOperationIssues(validOperation({ topic: "acme/orders/{}/status" }) as never);
        expect(issues.some(m => m.includes("empty dynamic level"))).toBe(true);
    });

    test("flags a dynamic level that is not a valid input property name", () => {
        const issues = eventMeshOperationIssues(validOperation({ topic: "acme/orders/{1bad}/status" }) as never);
        expect(issues.some(m => m.includes("not a valid input property name"))).toBe(true);
    });

    test("flags unbalanced braces in the topic", () => {
        const issues = eventMeshOperationIssues(validOperation({ topic: "acme/orders/{order_id/status" }) as never);
        expect(issues.some(m => m.includes("unbalanced dynamic level"))).toBe(true);
    });

    test("rejects a required parameter that is sourced from context", () => {
        const issues = eventMeshOperationIssues(
            validOperation({
                topic: "acme/orders/v1/status",
                input_schema: {
                    type: "object",
                    required: ["order_id"],
                    properties: { order_id: { type: "string", description: "id", "x-solace-context-expression": "user_config:orderId" } },
                },
            }) as never
        );
        expect(issues.some(m => m.includes("cannot be required from the agent"))).toBe(true);
    });

    test("rejects enum values that do not match the parameter type", () => {
        const issues = eventMeshOperationIssues(
            validOperation({
                topic: "acme/orders/v1/status",
                input_schema: {
                    type: "object",
                    required: ["order_id"],
                    properties: { order_id: { type: "string", description: "id", enum: [123] } },
                },
            }) as never
        );
        expect(issues.some(m => m.includes("Allowed values must match"))).toBe(true);
    });

    test("requires exactly one payload mapping for plain-text payloads", () => {
        const issues = eventMeshOperationIssues(
            validOperation({
                payload_format: "text",
                topic: "acme/orders/v1/{order_id}/status",
                input_schema: {
                    type: "object",
                    required: ["order_id"],
                    properties: { order_id: { type: "string", description: "id" } },
                },
            }) as never
        );
        expect(issues.some(m => m.includes("exactly one payload mapping"))).toBe(true);
    });

    test("reports an issue for an invalid tool name", () => {
        const issues = eventMeshOperationIssues(validOperation({ tool_name: "BadName" }) as never);
        expect(issues.length).toBeGreaterThan(0);
    });

    test("ignores an out-of-range timeout in No-Wait mode", () => {
        const issues = eventMeshOperationIssues(validOperation({ wait_for_response: false, request_expiry_ms: 10 }) as never);
        expect(issues.some(m => m.includes("Timeout must be between"))).toBe(false);
    });

    test("flags an out-of-range timeout in request-reply mode", () => {
        const issues = eventMeshOperationIssues(validOperation({ wait_for_response: true, request_expiry_ms: 10 }) as never);
        expect(issues.some(m => m.includes("Timeout must be between"))).toBe(true);
    });

    test("blocks navigation when the input schema draft is invalid", () => {
        const issues = eventMeshOperationIssues(validOperation({ _input_schema_invalid: true }) as never);
        expect(issues.some(m => m.includes("Input Schema contains invalid JSON"))).toBe(true);
    });
});

describe("eventMeshOperationFieldError", () => {
    test("returns the tool-name message for an invalid name and empty for a valid one", () => {
        expect(eventMeshOperationFieldError(validOperation({ tool_name: "BadName" }) as never, "tool_name")).toMatch(/lowercase/i);
        expect(eventMeshOperationFieldError(validOperation() as never, "tool_name")).toBe("");
    });
});

describe("eventMeshDuplicateToolNameUids", () => {
    const op = (uid: string, tool_name: string) => ({ ...newEventMeshOperation(), _uid: uid, tool_name });

    test("returns every uid sharing a tool name", () => {
        const dupes = eventMeshDuplicateToolNameUids([op("a", "send"), op("b", "send"), op("c", "unique")]);
        expect([...dupes].sort()).toEqual(["a", "b"]);
    });

    test("returns empty when all names are distinct", () => {
        expect(eventMeshDuplicateToolNameUids([op("a", "one"), op("b", "two")]).size).toBe(0);
    });

    test("ignores blank names so half-typed tools are not falsely flagged", () => {
        expect(eventMeshDuplicateToolNameUids([op("a", ""), op("b", "")]).size).toBe(0);
    });
});

describe("eventMeshPlaceholders", () => {
    test("extracts single-brace dynamic levels", () => {
        expect(eventMeshPlaceholders("acme/{region}/orders/{order_id}")).toEqual(["region", "order_id"]);
    });

    test("extracts legacy double-brace and scheme-embedded names", () => {
        expect(eventMeshPlaceholders("acme/{{ region }}/orders/{{ text://order_id }}")).toEqual(["region", "order_id"]);
    });

    test("ignores empty dynamic levels", () => {
        expect(eventMeshPlaceholders("acme/{}/orders")).toEqual([]);
    });
});

describe("stripEventMeshUiFields", () => {
    test("removes the UI-only _uid from operations but keeps other values", () => {
        const cleaned = stripEventMeshUiFields({ broker_url: "tcps://b:55443", operations: [{ _uid: "abc", tool_name: "get_order", topic: "a/b" }] });
        expect(cleaned.operations[0]).not.toHaveProperty("_uid");
        expect(cleaned.operations[0]).toMatchObject({ tool_name: "get_order", topic: "a/b" });
        expect(cleaned.broker_url).toBe("tcps://b:55443");
    });

    test("is a no-op when operations is absent", () => {
        expect(stripEventMeshUiFields({ broker_url: "x" })).toEqual({ broker_url: "x" });
    });
});
