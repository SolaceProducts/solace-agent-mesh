// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

type JsonObject = Record<string, unknown>;

interface InputField {
    name: string;
    types: string[];
}

interface InputShape {
    required: InputField[];
    optional: InputField[];
}

function isObject(value: unknown): value is JsonObject {
    return value !== null && typeof value === "object" && !Array.isArray(value);
}

function declaredProperties(schema: JsonObject): JsonObject {
    return isObject(schema.properties) ? schema.properties : {};
}

function requiredNames(schema: JsonObject): string[] {
    return Array.isArray(schema.required) ? schema.required.filter((name): name is string => typeof name === "string") : [];
}

function declaredTypes(property: unknown): string[] {
    if (!isObject(property)) return [];
    const type = property.type;
    if (typeof type === "string") return [type];
    return Array.isArray(type) ? type.filter((t): t is string => typeof t === "string") : [];
}

export function structuredInputSchema(schema: unknown): JsonObject | undefined {
    if (!isObject(schema)) return undefined;
    const asksForFields = Object.keys(declaredProperties(schema)).length > 0 || requiredNames(schema).length > 0;
    return asksForFields && !textSatisfies("text", schema) ? schema : undefined;
}

export function describeInputShape(schema: JsonObject): InputShape {
    const properties = declaredProperties(schema);
    const requiredList = requiredNames(schema);
    const field = (name: string): InputField => ({ name, types: declaredTypes(properties[name]).filter(t => t !== "null") });
    return {
        required: requiredList.map(field),
        optional: Object.keys(properties)
            .filter(name => !requiredList.includes(name))
            .toSorted((a, b) => a.localeCompare(b))
            .map(field),
    };
}

export function formatInputField({ name, types }: InputField): string {
    return types.length > 0 ? `${name} (${types.join(" or ")})` : name;
}

function placeholderFor(property: unknown): unknown {
    if (!isObject(property)) return "";
    if ("default" in property) return property.default;
    if ("const" in property) return property.const;
    if (Array.isArray(property.enum) && property.enum.length > 0) return property.enum[0];

    const type = declaredTypes(property).find(t => t !== "null") ?? (isObject(property.properties) ? "object" : "string");
    switch (type) {
        case "number":
        case "integer":
            return 0;
        case "boolean":
            return false;
        case "array":
            return [];
        case "object":
            return objectTemplate(property);
        default:
            return "";
    }
}

function objectTemplate(schema: JsonObject): JsonObject {
    const properties = declaredProperties(schema);
    return Object.fromEntries(requiredNames(schema).map(name => [name, placeholderFor(properties[name])]));
}

export function buildInputTemplate(schema: JsonObject): string {
    return JSON.stringify(objectTemplate(schema), null, 2);
}

export function messageAfterTargetChange(taskMessage: string, previousSchema: JsonObject | undefined, nextSchema: JsonObject | undefined): string {
    const untouched = !taskMessage.trim() || (previousSchema !== undefined && taskMessage === buildInputTemplate(previousSchema));
    if (!untouched) return taskMessage;
    return nextSchema ? buildInputTemplate(nextSchema) : "";
}

function matchesType(value: unknown, type: string): boolean {
    switch (type) {
        case "string":
            return typeof value === "string";
        case "number":
            return typeof value === "number";
        case "integer":
            return Number.isInteger(value);
        case "boolean":
            return typeof value === "boolean";
        case "array":
            return Array.isArray(value);
        case "object":
            return isObject(value);
        case "null":
            return value === null;
        default:
            return true;
    }
}

const quoted = (name: string): string => `"${name}"`;

function describeJsonType(value: unknown): string {
    if (value === null) return "null";
    return Array.isArray(value) ? "array" : typeof value;
}

function canonicalJson(value: unknown): string {
    if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
    if (isObject(value)) {
        const entries = Object.keys(value)
            .toSorted((a, b) => a.localeCompare(b))
            .map(key => `${JSON.stringify(key)}:${canonicalJson(value[key])}`);
        return `{${entries.join(",")}}`;
    }
    return JSON.stringify(value);
}

function fieldProblem(name: string, value: unknown, property: unknown): string | null {
    if (!isObject(property)) return null;
    const types = declaredTypes(property);
    if (types.length > 0 && !types.some(type => matchesType(value, type))) {
        return `"${name}" must be ${types.join(" or ")}, not ${describeJsonType(value)}`;
    }
    if (Array.isArray(property.enum) && !property.enum.some(allowed => canonicalJson(allowed) === canonicalJson(value))) {
        return `"${name}" must be one of ${property.enum.map(allowed => JSON.stringify(allowed)).join(", ")}`;
    }
    return null;
}

function inputProblems(value: JsonObject, schema: JsonObject): string[] {
    const properties = declaredProperties(schema);
    const problems: string[] = [];
    const missing = requiredNames(schema).filter(name => !Object.hasOwn(value, name));
    if (missing.length > 0) {
        problems.push(`missing ${missing.map(quoted).join(", ")}`);
    }
    for (const [name, fieldValue] of Object.entries(value)) {
        if (schema.additionalProperties === false && !Object.hasOwn(properties, name)) {
            problems.push(`${quoted(name)} is not a field of this workflow`);
            continue;
        }
        const problem = fieldProblem(name, fieldValue, properties[name]);
        if (problem) problems.push(problem);
    }
    return problems;
}

function textSatisfies(text: string, schema: JsonObject): boolean {
    return inputProblems({ text }, schema).length === 0;
}

function parseJsonObject(text: string): JsonObject | undefined {
    const trimmed = text.trim();
    if (!trimmed.startsWith("{")) return undefined;
    try {
        const parsed: unknown = JSON.parse(trimmed);
        return isObject(parsed) ? parsed : undefined;
    } catch {
        return undefined;
    }
}

function isInputDocument(doc: JsonObject, schema: JsonObject): boolean {
    const declared = Object.keys(declaredProperties(schema));
    const names = Object.keys(doc);
    return names.length > 0 && declared.length > 0 && names.every(name => declared.includes(name)) && inputProblems(doc, schema).length === 0;
}

function requiredShapeHint(schema: JsonObject): string {
    const { required } = describeInputShape(schema);
    return required.length > 0 ? ` Required: ${required.map(formatInputField).join(", ")}.` : "";
}

function notAnObjectProblem(text: string, schema: JsonObject): string {
    let detail = "";
    if (text.trim().startsWith("{")) {
        try {
            JSON.parse(text.trim());
        } catch (error) {
            detail = ` (${error instanceof Error ? error.message : "not valid JSON"})`;
        }
    }
    return `This workflow takes structured input: enter a JSON object${detail}.${requiredShapeHint(schema)}`;
}

export function validateWorkflowInput(text: string, schema: JsonObject): string | null {
    const doc = parseJsonObject(text);
    if (doc && isInputDocument(doc, schema)) return null;
    if (textSatisfies(text, schema)) return null;
    if (!doc) return notAnObjectProblem(text, schema);

    const problems = inputProblems(doc, schema);
    return problems.length > 0 ? `The input does not fit this workflow: ${problems.join("; ")}.` : null;
}
