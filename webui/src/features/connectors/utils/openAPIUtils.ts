// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { OpenAPISpecFile, Path, Tool } from "@/types";

const toSnakeCase = (str: string): string => {
    return str
        .replace(/([a-z])([A-Z])/g, "$1_$2")
        .replace(/[\s-]+/g, "_")
        .replace(/[^\w]/g, "_")
        .replace(/_+/g, "_")
        .replace(/^_+|_+$/g, "")
        .substring(0, 60)
        .toLowerCase();
};

const getOperationId = (method: string, endpoint: string, path: Path, existingTools: Tool[]): string => {
    let operationId: string;
    if (path.operationId) {
        operationId = path.operationId;
    } else {
        operationId = toSnakeCase(`${endpoint}_${method}`);
    }

    return operationId + (existingTools.some(tool => tool.operationId === operationId) ? `_${existingTools.length + 1}` : "");
};

const isPrimitive = (type: string): boolean => ["string", "integer", "number", "boolean"].includes(type);

const reservedWords = new Set(["description", "enum", "parameters", "summary", "tags"]);

export const patchDescription = (description: string): string => {
    return description
        .replace(/<a href="[^"]+"[^>]*>([^<]+)<\/a>/g, "$1")
        .replace(/<\/?\w+>/g, "")
        .trim();
};

export const patchDescriptions = <T extends { description?: string }>(items: T[]): T[] => {
    return items.map(item => ({
        ...item,
        description: item.description ? patchDescription(item.description) : item.description,
    }));
};

export const getSchemaType = (schema: any, spec: OpenAPISpecFile): string | null => {
    if (schema) {
        if (schema.type) {
            if (schema.type === "array" && schema.items) {
                if (schema.items.type) {
                    return `${schema.items.type}[]`;
                }
                if (schema.items.$ref) {
                    return `${getSchemaType(findSchemaByRef(schema.items.$ref, spec), spec)}[]`;
                }
                return "array";
            }
            return schema.type;
        } else if (schema.$ref) {
            return getSchemaType(findSchemaByRef(schema.$ref, spec), spec);
        }
    }
    return null;
};

export const findSchemaByRef = (ref: string, spec: OpenAPISpecFile): any => {
    const refPath = ref.replace(/^#\//, "").split("/");
    let schema: any = spec;
    for (const part of refPath) {
        schema = schema[part];
    }
    return schema;
};

const resolveLocalReference = (ref: unknown, spec: OpenAPISpecFile): any => {
    if (typeof ref !== "string" || !ref.startsWith("#/")) {
        return undefined;
    }
    try {
        return findSchemaByRef(ref, spec);
    } catch {
        return undefined;
    }
};

const resolveParameter = (param: any, spec: OpenAPISpecFile, operation: string): any => {
    if (!param?.$ref) {
        return param;
    }
    const resolved = resolveLocalReference(param.$ref, spec);
    if (!resolved) {
        throw new Error(`${operation}: the parameter reference "${param.$ref}" cannot be resolved. Only references inside the specification ("#/...") are supported.`);
    }
    return resolved;
};

const operationParameters = (path: Path, commonParameters: any[], operation: string): any[] => {
    if (path.parameters != null && !Array.isArray(path.parameters)) {
        throw new Error(`${operation}: "parameters" must be a list.`);
    }
    return [...commonParameters, ...(path.parameters ?? [])];
};

const buildTool = (method: string, endpoint: string, path: Path | null, commonParameters: any[], spec: OpenAPISpecFile, existingTools: Tool[]): Tool => {
    const operation = `${method.toUpperCase()} ${endpoint}`;
    if (path === null) {
        throw new Error(`${operation} must be an object.`);
    }
    const operationId = getOperationId(method, endpoint, path, existingTools);
    const parameters = operationParameters(path, commonParameters, operation).map(param => resolveParameter(param, spec, operation));

    return {
        operationId,
        name: path.summary || operationId,
        description: patchDescription(path.description || ""),
        method: method.toUpperCase(),
        endpoint: endpoint,
        parameters: patchDescriptions(parameters),
        requestBody: path.requestBody || null,
        deprecated: path.deprecated,
    };
};

export const extractTools = (spec: OpenAPISpecFile): Tool[] => {
    if (!spec?.paths) {
        return [];
    }

    const extractedTools: Tool[] = [];
    let commonParameters: any[] = [];
    for (const [endpoint, methods] of Object.entries<any>(spec.paths)) {
        if (methods === null || typeof methods !== "object") {
            throw new Error(`Path "${endpoint}" must be an object.`);
        }
        for (const [method, path] of Object.entries<Path | null>(methods)) {
            if (reservedWords.has(method)) {
                if (method === "parameters" && Array.isArray(path)) {
                    commonParameters = path as any[];
                }
                continue;
            }
            extractedTools.push(buildTool(method, endpoint, path, commonParameters, spec, extractedTools));
        }
    }
    return extractedTools;
};

export function generateExampleFromSchema(schema: any, spec: OpenAPISpecFile, visitedRefs: Set<string> = new Set()): any {
    if (!schema) return null;
    if (Array.isArray(schema.examples) && schema.examples.length > 0) return schema.examples[0];
    if (schema.example !== undefined) return schema.example;
    if (schema.default !== undefined) return schema.default;

    if ((schema.oneOf || schema.anyOf || schema.allOf || schema.$ref) && !isPrimitive(schema.type)) {
        let obj: any = {};
        if (schema.oneOf) {
            obj = generateExampleFromSchema(schema.oneOf[0], spec, visitedRefs);
        } else if (schema.anyOf) {
            obj = generateExampleFromSchema(schema.anyOf[0], spec, visitedRefs);
        } else if (schema.allOf) {
            const combinedSchema = Object.assign({}, ...schema.allOf);
            obj = generateExampleFromSchema(combinedSchema, spec, visitedRefs);
        } else if (schema.$ref) {
            const refKey = schema.$ref;
            if (visitedRefs.has(refKey)) {
                return {};
            }
            visitedRefs.add(refKey);
            obj = generateExampleFromSchema(findSchemaByRef(refKey, spec), spec, visitedRefs);
            visitedRefs.delete(refKey);
        }

        if (schema.type) {
            const { oneOf, anyOf, allOf, $ref, ...rest } = schema;
            Object.assign(obj, generateExampleFromSchema({ ...rest }, spec, visitedRefs));
        }
        return obj;
    }

    if (schema.type === "object") {
        if (schema.properties) {
            const obj: any = {};
            for (const [key, propSchema] of Object.entries(schema.properties)) {
                obj[key] = generateExampleFromSchema(propSchema, spec, visitedRefs);
            }
            return obj;
        } else if (schema.additionalProperties) {
            if (typeof schema.additionalProperties === "boolean") {
                if (schema.additionalProperties === true) {
                    return {};
                }
                return undefined;
            }
            return generateExampleFromSchema(schema.additionalProperties, spec, visitedRefs);
        }
    }

    if (schema.type === "array" && schema.items) {
        return [generateExampleFromSchema(schema.items, spec, visitedRefs)];
    }

    if (Array.isArray(schema.type)) {
        return generateExampleFromSchema({ ...schema, type: schema.type[0] }, spec, visitedRefs);
    }

    switch (schema.type) {
        case "string":
            if (Array.isArray(schema.enum)) return schema.enum[0];
            if (schema.format === "date-time") return "2023-01-01T00:00:00Z";
            if (schema.format === "date") return "2023-01-01";
            if (schema.format === "uuid") return "123e4567-e89b-12d3-a456-426614174000";
            return "string";
        case "integer":
        case "number":
            return 0;
        case "boolean":
            return true;
        default:
            return null;
    }
}

export function generateRequestBodyExample(requestBody: any, spec: OpenAPISpecFile): any {
    const jsonContent = requestBody?.content?.["application/json"];

    if (jsonContent?.example) {
        return jsonContent.example.$ref ? findSchemaByRef(jsonContent.example.$ref, spec) : jsonContent.example;
    } else if (jsonContent?.examples) {
        let value: any;
        if (Array.isArray(jsonContent.examples) && jsonContent.examples.length > 0) {
            value = jsonContent.examples[0]?.value;
        } else {
            const firstExample = Object.values(jsonContent.examples)[0] ?? ({} as any);
            value = firstExample?.value;
        }
        if (value !== undefined) {
            if (typeof value === "string") {
                try {
                    return JSON.parse(value);
                } catch {}
            }
            return value;
        }
    }
    if (jsonContent?.schema) {
        return generateExampleFromSchema(jsonContent.schema, spec);
    }
    return null;
}
