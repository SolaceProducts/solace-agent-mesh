// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { parseSpecificationText, PARSING_ERROR_TITLE, type SpecificationParseResult, UNSUPPORTED_VERSION_TITLE, validateOpenAPIVersion } from "./parseSpecification";

const failureOf = (result: SpecificationParseResult) => (result.ok ? undefined : result.failure);
const specOf = (result: SpecificationParseResult) => (result.ok ? result.spec : undefined);
const toolsOf = (result: SpecificationParseResult) => (result.ok ? result.tools : undefined);

const PETS_JSON = JSON.stringify({
    openapi: "3.1.0",
    info: { title: "Pets", version: "1" },
    paths: { "/pets": { get: { operationId: "listPets", summary: "List pets", parameters: [{ name: "limit", in: "query", required: false }] } } },
});

describe("parseSpecificationText", () => {
    it("returns the spec and its tools for a valid JSON document", () => {
        const result = parseSpecificationText(PETS_JSON, false);

        expect(failureOf(result)).toBeUndefined();
        expect(specOf(result)?.info.title).toBe("Pets");
        expect(toolsOf(result)?.map(tool => tool.operationId)).toEqual(["listPets"]);
    });

    it("returns the spec and its tools for a valid YAML document", () => {
        const result = parseSpecificationText('openapi: "3.0.3"\ninfo:\n  title: Pets\npaths:\n  /pets:\n    get:\n      operationId: listPets\n', true);

        expect(failureOf(result)).toBeUndefined();
        expect(toolsOf(result)).toHaveLength(1);
    });

    it("opens the parsing error for an unquoted YAML version, which the platform's spec loader rejects", () => {
        const result = parseSpecificationText("openapi: 3.1\ninfo:\n  title: Pets\npaths: {}\n", true);

        expect(failureOf(result)?.title).toBe(PARSING_ERROR_TITLE);
        expect(failureOf(result)?.message).toContain('must be a quoted string such as "3.1.0"');
        expect(failureOf(result)?.message).toContain("3.1");
    });

    it.each([
        ["not JSON", "{not json", false, "Failed to parse JSON specification"],
        ["not YAML", "a: [unclosed", true, "Failed to parse YAML specification"],
        ["a JSON array", "[1, 2]", false, "does not contain an OpenAPI specification object"],
        ["a JSON number", "3", false, "does not contain an OpenAPI specification object"],
        ["JSON null", "null", false, "does not contain an OpenAPI specification object"],
        ["an empty YAML file", "", true, "does not contain an OpenAPI specification object"],
        ["a YAML scalar", "just text", true, "does not contain an OpenAPI specification object"],
    ])("opens the parsing error for %s", (_label, text, isYaml, expected) => {
        const result = parseSpecificationText(text, isYaml);

        expect(failureOf(result)?.title).toBe(PARSING_ERROR_TITLE);
        expect(failureOf(result)?.message).toContain(expected);
        expect(specOf(result)).toBeUndefined();
    });

    it.each([
        ["an external parameter $ref", { "/pets": { get: { parameters: [{ $ref: "https://example.test/params.yaml#/limit" }] } } }, 'GET /pets: the parameter reference "https://example.test/params.yaml#/limit" cannot be resolved'],
        ["a relative parameter $ref", { "/pets": { get: { parameters: [{ $ref: "./params.yaml#/limit" }] } } }, "cannot be resolved"],
        ["a local parameter $ref that points nowhere", { "/pets": { get: { parameters: [{ $ref: "#/components/parameters/Missing" }] } } }, "cannot be resolved"],
        ["a null path item", { "/pets": null }, 'Path "/pets" must be an object'],
        ["a null operation", { "/pets": { get: null } }, "GET /pets must be an object"],
        ["operation parameters that are not a list", { "/pets": { get: { parameters: { name: "limit" } } } }, 'GET /pets: "parameters" must be a list'],
    ])("opens the parsing error when extracting tools fails on %s", (_label, paths, expected) => {
        const result = parseSpecificationText(JSON.stringify({ openapi: "3.0.0", info: { title: "Pets" }, paths }), false);

        expect(failureOf(result)?.title).toBe(PARSING_ERROR_TITLE);
        expect(failureOf(result)?.message).toContain("Failed to read the operations from the specification");
        expect(failureOf(result)?.message).toContain(expected);
    });

    it("treats a null parameters key as no parameters, as the platform's spec loader does", () => {
        const parameters = null;
        const spec = { openapi: "3.0.0", info: { title: "Pets" }, paths: { "/pets": { get: { operationId: "listPets", parameters } } } };

        const result = parseSpecificationText(JSON.stringify(spec), false);

        expect(result.ok).toBe(true);
        expect(toolsOf(result)?.[0]?.parameters).toEqual([]);
    });

    it("keeps resolving parameter references inside the document", () => {
        const spec = {
            openapi: "3.0.0",
            info: { title: "Pets" },
            paths: { "/pets": { get: { operationId: "listPets", parameters: [{ $ref: "#/components/parameters/Limit" }] } } },
            components: { parameters: { Limit: { name: "limit", in: "query", required: false } } },
        };

        const result = parseSpecificationText(JSON.stringify(spec), false);

        expect(failureOf(result)).toBeUndefined();
        expect(toolsOf(result)?.[0]?.parameters?.map(p => p.name)).toEqual(["limit"]);
    });

    it("reports a hand-edited JSON file the platform labelled YAML as a JSON error", () => {
        const result = parseSpecificationText('{"openapi": "3.0.0", "info": {"title": "x"} "paths": {}}', true);

        const message = failureOf(result)?.message ?? "";
        expect(message).toContain("Failed to parse JSON specification");
        expect(message).not.toContain("flow collection");
    });

    it("keeps the line and column js-yaml reports for a malformed block-YAML file", () => {
        const result = parseSpecificationText("openapi: '3.0.0'\ninfo: {title: a}\ninfo: {title: b}\n", true);

        expect(failureOf(result)?.message).toMatch(/Failed to parse YAML specification: .*\(\d+:\d+\)/);
    });

    it("still reads a flow-style YAML mapping the platform labelled YAML", () => {
        expect(parseSpecificationText("{openapi: '3.0.0', info: {title: x}, paths: {}}", true).ok).toBe(true);
    });

    it("does not reject a path item that carries a servers list", () => {
        const spec = { openapi: "3.0.0", info: { title: "Pets" }, paths: { "/pets": { servers: [{ url: "https://example.test" }], get: { operationId: "listPets" } } } };

        expect(parseSpecificationText(JSON.stringify(spec), false).ok).toBe(true);
    });
});

describe("validateOpenAPIVersion", () => {
    it("accepts any 3.x version string", () => {
        expect(validateOpenAPIVersion({ openapi: "3.0.0" })).toBeNull();
        expect(validateOpenAPIVersion({ openapi: "3.1.1" })).toBeNull();
    });

    it("refuses Swagger 2.0 with the unsupported-version title", () => {
        const failure = validateOpenAPIVersion({ swagger: "2.0" });

        expect(failure?.title).toBe(UNSUPPORTED_VERSION_TITLE);
        expect(failure?.message).toContain("Swagger 2.0");
    });

    it("refuses a non-3.x version with the unsupported-version title", () => {
        expect(validateOpenAPIVersion({ openapi: "4.0.0" })?.title).toBe(UNSUPPORTED_VERSION_TITLE);
    });

    it.each([undefined, null, ""])("names the missing version field when it is %s", version => {
        expect(validateOpenAPIVersion({ openapi: version })?.message).toContain("missing 'openapi' version field");
    });

    it("refuses a numeric version without throwing", () => {
        expect(validateOpenAPIVersion({ openapi: 3.1 })?.title).toBe(PARSING_ERROR_TITLE);
    });
});
