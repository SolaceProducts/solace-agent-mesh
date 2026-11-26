// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import jsyaml from "js-yaml";

import type { OpenAPISpecFile, Tool } from "@/types";

import { extractTools } from "./openAPIUtils";

export const PARSING_ERROR_TITLE = "OpenAPI Parsing Error";
export const UNSUPPORTED_VERSION_TITLE = "Unsupported OpenAPI Version";

export interface SpecificationFailure {
    title: string;
    message: string;
}

export interface ParsedSpecification {
    spec: OpenAPISpecFile;
    tools: Tool[];
}

export type SpecificationParseResult = ({ ok: true } & ParsedSpecification) | { ok: false; failure: SpecificationFailure };

const parsingFailure = (message: string): { ok: false; failure: SpecificationFailure } => ({ ok: false, failure: { title: PARSING_ERROR_TITLE, message } });

const describeError = (error: unknown): string => (error instanceof Error ? error.message : String(error));

export const validateOpenAPIVersion = (spec: Record<string, unknown>): SpecificationFailure | null => {
    if ("swagger" in spec) {
        return {
            title: UNSUPPORTED_VERSION_TITLE,
            message: `This appears to be a Swagger 2.0 specification (found 'swagger: ${String(spec.swagger)}'). Agent Mesh supports OpenAPI 3.x specifications only. Please convert your specification using https://converter.swagger.io`,
        };
    }

    const version = spec.openapi;
    if (version === undefined || version === null || version === "") {
        return {
            title: PARSING_ERROR_TITLE,
            message: "The uploaded file does not appear to be a valid OpenAPI specification (missing 'openapi' version field). Please ensure the file is an OpenAPI 3.x specification.",
        };
    }

    if (typeof version !== "string") {
        return {
            title: PARSING_ERROR_TITLE,
            message: `The 'openapi' version must be a quoted string such as "3.1.0", but the file has the ${typeof version} ${JSON.stringify(version)}. In YAML, write openapi: "3.1.0".`,
        };
    }

    if (!version.startsWith("3.")) {
        return {
            title: UNSUPPORTED_VERSION_TITLE,
            message: `OpenAPI version ${version} is not supported. Agent Mesh supports OpenAPI 3.x specifications only. Please convert your specification to OpenAPI 3.x.`,
        };
    }

    return null;
};

type ParsedDocument = { ok: true; document: Record<string, unknown> } | { ok: false; failure: SpecificationFailure };

function parseText(text: string, isYaml: boolean): unknown {
    if (!isYaml) return JSON.parse(text);
    if (!text.trimStart().startsWith("{")) return jsyaml.load(text);
    try {
        return JSON.parse(text);
    } catch (jsonError) {
        try {
            return jsyaml.load(text);
        } catch {
            throw jsonError;
        }
    }
}

function parseDocument(text: string, isYaml: boolean): ParsedDocument {
    const format = isYaml && !text.trimStart().startsWith("{") ? "YAML" : "JSON";
    let parsed: unknown;
    try {
        parsed = parseText(text, isYaml);
    } catch (error) {
        return parsingFailure(`Failed to parse ${format} specification: ${describeError(error)}`);
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
        return parsingFailure(`The ${format} file does not contain an OpenAPI specification object.`);
    }
    return { ok: true, document: parsed as Record<string, unknown> };
}

export function parseSpecificationText(text: string, isYaml: boolean): SpecificationParseResult {
    const parsed = parseDocument(text, isYaml);
    if (!parsed.ok) {
        return parsed;
    }

    const versionFailure = validateOpenAPIVersion(parsed.document);
    if (versionFailure) {
        return { ok: false, failure: versionFailure };
    }

    const spec = parsed.document as OpenAPISpecFile;
    try {
        return { ok: true, spec, tools: extractTools(spec) };
    } catch (error) {
        return parsingFailure(`Failed to read the operations from the specification: ${describeError(error)}`);
    }
}
