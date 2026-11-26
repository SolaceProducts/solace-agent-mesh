// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

import { baseConnectorSchema } from "./utils";

export type SelectOption = { value: string; label: string };

export const EVENT_MESH_RESPONSE_MODE_OPTIONS: SelectOption[] = [
    { value: "request_reply", label: "Request-Reply (Wait for response)" },
    { value: "publish_subscribe", label: "Publish-Subscribe (No wait)" },
];

export const EVENT_MESH_PAYLOAD_FORMAT_OPTIONS: SelectOption[] = [
    { value: "json", label: "JSON" },
    { value: "yaml", label: "YAML" },
    { value: "text", label: "Plain text" },
];

export const EVENT_MESH_DELIVERY_MODE_OPTIONS: SelectOption[] = [
    { value: "1", label: "Guaranteed Messaging" },
    { value: "0", label: "Direct Messaging" },
];

export const EVENT_MESH_REPLY_MODE_OPTIONS: SelectOption[] = [
    { value: "temp_queue", label: "Temporary Queue" },
    { value: "p2p_inbox", label: "Direct Reply-To" },
];

export interface EventMeshParameter {
    _uid?: string;
    name: string;
    type: "string" | "integer" | "number" | "boolean";
    required?: boolean;
    description?: string;
    enum?: unknown[];
    payload_path?: string;
    default?: unknown;
    context_expression?: string;
    [key: string]: unknown;
}

export interface EventMeshInputSchema {
    type: "object";
    properties: Record<string, Record<string, unknown>>;
    required?: string[];
    [key: string]: unknown;
}

export interface EventMeshOperation {
    _uid?: string;
    tool_name: string;
    tool_description: string;
    topic: string;
    wait_for_response: boolean;
    request_expiry_ms?: number;
    reply_mode?: "temp_queue" | "p2p_inbox";
    qos?: 0 | 1;
    payload_format: "json" | "yaml" | "text";
    input_schema: EventMeshInputSchema;
    [key: string]: unknown;
}

export function responseModeValue(waitForResponse: boolean | undefined): string {
    return waitForResponse ? "request_reply" : "publish_subscribe";
}

let uidSeq = 0;

export const newUid = (): string => {
    if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
        return crypto.randomUUID();
    }
    uidSeq += 1;
    return `op-${uidSeq}`;
};

export function newEventMeshParameter(): EventMeshParameter {
    return {
        name: "",
        type: "string",
        required: false,
        description: "",
        payload_path: "",
    };
}

export function newEventMeshOperation(_index = 0): EventMeshOperation {
    return {
        _uid: newUid(),
        tool_name: "",
        tool_description: "",
        topic: "",
        wait_for_response: true,
        request_expiry_ms: 60000,
        reply_mode: "temp_queue",
        qos: 1,
        payload_format: "json",
        input_schema: {
            type: "object",
            properties: {},
        },
    };
}

function normalizeInputSchema(raw: unknown): EventMeshInputSchema {
    if (raw && typeof raw === "object" && !Array.isArray(raw)) {
        const schema = raw as Record<string, unknown>;
        return {
            ...schema,
            type: "object",
            properties: schema.properties && typeof schema.properties === "object" && !Array.isArray(schema.properties) ? (schema.properties as Record<string, Record<string, unknown>>) : {},
            required: Array.isArray(schema.required) ? schema.required.filter(value => typeof value === "string") : undefined,
        };
    }
    return { type: "object", properties: {} };
}

function normalizeOperation(raw: Record<string, unknown>, index: number): EventMeshOperation {
    const defaults = newEventMeshOperation(index);
    return {
        ...raw,
        _uid: typeof raw._uid === "string" ? raw._uid : defaults._uid,
        tool_name: typeof raw.tool_name === "string" ? raw.tool_name : defaults.tool_name,
        tool_description: typeof raw.tool_description === "string" ? raw.tool_description : defaults.tool_description,
        topic: typeof raw.topic === "string" ? raw.topic : defaults.topic,
        wait_for_response: raw.wait_for_response !== false && raw.wait_for_response !== "false",
        request_expiry_ms: typeof raw.request_expiry_ms === "number" ? raw.request_expiry_ms : defaults.request_expiry_ms,
        reply_mode: raw.reply_mode === "p2p_inbox" ? "p2p_inbox" : "temp_queue",
        qos: raw.qos === 0 || raw.qos === "0" ? 0 : 1,
        payload_format: raw.payload_format === "yaml" || raw.payload_format === "text" ? raw.payload_format : "json",
        input_schema: normalizeInputSchema(raw.input_schema),
    };
}

export function normalizeEventMeshValues(values: Record<string, any> = {}): Record<string, any> {
    return {
        ...values,
        operations: Array.isArray(values.operations) ? values.operations.map((operation, index) => normalizeOperation(operation ?? {}, index)) : [newEventMeshOperation()],
    };
}

export function stripEventMeshUiFields(values: Record<string, any> = {}): Record<string, any> {
    if (!Array.isArray(values.operations)) return values;
    return {
        ...values,
        operations: values.operations.map((operation: EventMeshOperation) => {
            const { _uid, _input_schema_invalid, ...rest } = operation ?? {};
            return rest;
        }),
    };
}

const parameterSchema = z
    .object({
        name: z
            .string()
            .min(1, "Parameter name is required")
            .regex(/^[A-Za-z_]\w*$/, "Use letters, numbers, and underscores"),
        type: z.enum(["string", "integer", "number", "boolean"]),
        required: z.boolean().optional(),
        description: z.string().optional(),
        enum: z.array(z.unknown()).min(1, "Allowed values cannot be empty").optional(),
        payload_path: z.string().optional(),
        default: z.unknown().optional(),
        context_expression: z.string().optional(),
    })
    .passthrough();

const inputSchemaSchema = z
    .object({
        type: z.literal("object"),
        properties: z.record(z.string(), z.record(z.string(), z.unknown())),
        required: z.array(z.string()).optional(),
    })
    .passthrough();

function enumHasTypeMismatch(parameter: EventMeshParameter): boolean {
    return !!parameter.enum?.some(value => typeof value !== parameter.type && !(parameter.type === "integer" && typeof value === "number" && Number.isInteger(value)));
}

function addParameterIssues(parameters: EventMeshParameter[], ctx: z.RefinementCtx): Set<string> {
    const names = new Set<string>();
    parameters.forEach((parameter, index) => {
        const parameterResult = parameterSchema.safeParse(parameter);
        if (!parameterResult.success) {
            parameterResult.error.issues.forEach(issue => {
                ctx.addIssue({ code: "custom", path: ["input_schema", "properties", parameter.name, ...issue.path], message: issue.message });
            });
        }
        if (names.has(parameter.name)) ctx.addIssue({ code: "custom", path: ["parameters", index, "name"], message: "Parameter names must be unique" });
        names.add(parameter.name);
        if (enumHasTypeMismatch(parameter)) {
            ctx.addIssue({ code: "custom", path: ["parameters", index, "enum"], message: `Allowed values must match the ${parameter.type} parameter type` });
        }
        if (parameter.context_expression && parameter.required) {
            ctx.addIssue({ code: "custom", path: ["parameters", index, "context_expression"], message: "Context-sourced parameters cannot be required from the agent" });
        }
    });
    return names;
}

function addTopicIssues(topic: string, names: Set<string>, ctx: z.RefinementCtx): void {
    const openBraces = (topic.match(/\{/g) ?? []).length;
    const closeBraces = (topic.match(/\}/g) ?? []).length;
    if (openBraces !== closeBraces) {
        ctx.addIssue({ code: "custom", path: ["topic"], message: "Topic address has an unbalanced dynamic level" });
    }
    for (const match of topic.matchAll(/\{\{([^{}]*)\}\}|\{([^{}]*)\}/g)) {
        const isDouble = match[0].startsWith("{{");
        let name = (isDouble ? match[1] : match[2]).trim();
        if (isDouble && name.includes("://")) name = name.slice(name.indexOf("//") + 2).trim();
        if (!name) ctx.addIssue({ code: "custom", path: ["topic"], message: "Topic address contains an empty dynamic level {}" });
        else if (!/^[A-Za-z_]\w*$/.test(name)) ctx.addIssue({ code: "custom", path: ["topic"], message: `Topic address dynamic level "${name}" is not a valid input property name` });
        else if (!names.has(name)) ctx.addIssue({ code: "custom", path: ["topic"], message: `Topic address uses undeclared input property "${name}"` });
    }
}

const operationSchema = z
    .object({
        tool_name: z
            .string()
            .min(1, "Tool name is required")
            .max(64)
            .regex(/^[a-z][a-z0-9_]*$/, "Use lowercase letters, numbers, and underscores"),
        tool_description: z.string().min(10, "Description must be at least 10 characters").max(2048),
        topic: z.string().min(1, "Topic address is required").max(512),
        wait_for_response: z.boolean(),
        request_expiry_ms: z.number().optional(),
        reply_mode: z.enum(["temp_queue", "p2p_inbox"]).optional(),
        qos: z.union([z.literal(0), z.literal(1)]).optional(),
        payload_format: z.enum(["json", "yaml", "text"]),
        input_schema: inputSchemaSchema,
    })
    .passthrough()
    .superRefine((operation, ctx) => {
        const parameters = eventMeshParametersFromSchema(operation.input_schema);
        const names = addParameterIssues(parameters, ctx);
        addTopicIssues(operation.topic, names, ctx);
        if (operation.payload_format === "text" && parameters.filter(parameter => parameter.payload_path).length !== 1) {
            ctx.addIssue({ code: "custom", path: ["input_schema"], message: "Plain text payloads require exactly one payload mapping" });
        }
        if (operation.wait_for_response && operation.request_expiry_ms !== undefined && (operation.request_expiry_ms < 1000 || operation.request_expiry_ms > 300000)) {
            ctx.addIssue({ code: "custom", path: ["request_expiry_ms"], message: "Timeout must be between 1000 and 300000 ms" });
        }
        if ((operation as Record<string, unknown>)._input_schema_invalid === true) {
            ctx.addIssue({ code: "custom", path: ["input_schema"], message: "Input Schema contains invalid JSON" });
        }
    });

export function createEventMeshConnectorSchema(isEditMode: boolean, validateOperations = true) {
    const operations = validateOperations
        ? z
              .array(operationSchema)
              .min(1, "Add at least one tool")
              .superRefine((operations, ctx) => {
                  const names = new Set<string>();
                  operations.forEach((operation, index) => {
                      if (names.has(operation.tool_name)) ctx.addIssue({ code: "custom", path: [index, "tool_name"], message: "Tool names must be unique" });
                      names.add(operation.tool_name);
                  });
              })
        : z.array(z.any()).optional();

    return baseConnectorSchema.extend({
        values: z
            .object({
                broker_url: z
                    .string()
                    .min(1, "Host URI is required")
                    .refine(value => value === "" || /^tcps?:\/\/.+/.test(value), "Host URI must start with tcp:// or tcps://"),
                broker_vpn: z.string().min(1, "Message VPN is required"),
                broker_username: z.string().min(1, "Username is required"),
                broker_password: isEditMode ? z.string().optional().nullable() : z.string().min(1, "Password is required"),
                operations,
            })
            .passthrough(),
    });
}

export function eventMeshPlaceholders(topic: string): string[] {
    const names: string[] = [];
    for (const match of topic.matchAll(/\{\{([^{}]*)\}\}|\{([^{}]*)\}/g)) {
        const isDouble = match[0].startsWith("{{");
        let name = (isDouble ? match[1] : match[2]).trim();
        if (isDouble && name.includes("://")) name = name.slice(name.indexOf("//") + 2).trim();
        if (name) names.push(name);
    }
    return names;
}

export function eventMeshOperationIssues(operation: EventMeshOperation): string[] {
    const result = operationSchema.safeParse(operation);
    return result.success ? [] : result.error.issues.map(issue => issue.message);
}

export function eventMeshOperationFieldError(operation: EventMeshOperation, field: keyof EventMeshOperation): string {
    const result = operationSchema.safeParse(operation);
    if (result.success) return "";
    return result.error.issues.find(issue => issue.path[0] === field)?.message ?? "";
}

export function eventMeshOperationSchemaError(operation: EventMeshOperation): string {
    const result = operationSchema.safeParse(operation);
    if (result.success) return "";
    return result.error.issues.find(issue => issue.path[0] === "input_schema" || issue.path[0] === "parameters")?.message ?? "";
}

export function eventMeshDuplicateToolNameUids(operations: EventMeshOperation[]): Set<string> {
    const uidsByName = new Map<string, string[]>();
    for (const operation of operations) {
        const name = operation.tool_name.trim();
        if (!name || !operation._uid) continue;
        uidsByName.set(name, [...(uidsByName.get(name) ?? []), operation._uid]);
    }
    const duplicates = new Set<string>();
    for (const uids of uidsByName.values()) {
        if (uids.length > 1) uids.forEach(uid => duplicates.add(uid));
    }
    return duplicates;
}

export function parseEventMeshInputSchemaJSON(text: string): EventMeshInputSchema {
    const parsed: unknown = JSON.parse(text);
    const result = inputSchemaSchema.safeParse(parsed);
    if (!result.success) {
        const issue = result.error.issues[0];
        const location = issue.path.length > 0 ? ` at ${issue.path.join(".")}` : "";
        throw new Error(`${issue.message}${location}`);
    }
    return withDefaultPayloadPaths(result.data as EventMeshInputSchema);
}

export function withDefaultPayloadPaths(schema: EventMeshInputSchema): EventMeshInputSchema {
    const properties = Object.fromEntries(
        Object.entries(schema.properties ?? {}).map(([name, property]) => {
            const payloadPath = property["x-solace-payload-path"];
            return [
                name,
                {
                    ...property,
                    "x-solace-payload-path": typeof payloadPath === "string" && payloadPath.trim() ? payloadPath : name,
                },
            ];
        })
    );

    return {
        ...schema,
        properties,
    };
}

export function eventMeshParametersFromSchema(schema: EventMeshInputSchema): EventMeshParameter[] {
    const required = new Set(schema.required ?? []);
    return Object.entries(schema.properties ?? {}).map(([name, property]) => {
        const { ["x-solace-payload-path"]: payloadPath, ["x-solace-context-expression"]: contextExpression, ...jsonSchema } = property;
        return {
            ...jsonSchema,
            name,
            type: jsonSchema.type === "integer" || jsonSchema.type === "number" || jsonSchema.type === "boolean" ? jsonSchema.type : "string",
            required: required.has(name),
            description: typeof jsonSchema.description === "string" ? jsonSchema.description : "",
            enum: Array.isArray(jsonSchema.enum) ? jsonSchema.enum : undefined,
            default: jsonSchema.default,
            payload_path: typeof payloadPath === "string" ? payloadPath : "",
            context_expression: typeof contextExpression === "string" ? contextExpression : "",
        };
    });
}

export function eventMeshSchemaFromParameters(parameters: EventMeshParameter[], previous?: EventMeshInputSchema): EventMeshInputSchema {
    const properties: Record<string, Record<string, unknown>> = {};
    const required: string[] = [];
    for (const parameter of parameters) {
        if (!parameter.name) continue;
        const { _uid, name, required: isRequired, payload_path, context_expression, ...schema } = parameter;
        properties[name] = {
            ...schema,
            ...(payload_path ? { "x-solace-payload-path": payload_path } : {}),
            ...(context_expression ? { "x-solace-context-expression": context_expression } : {}),
        };
        if (isRequired && !context_expression) required.push(name);
    }
    return {
        ...previous,
        type: "object",
        properties,
        ...(required.length > 0 ? { required } : { required: undefined }),
    };
}
