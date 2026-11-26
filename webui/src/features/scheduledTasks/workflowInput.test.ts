// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { buildInputTemplate, describeInputShape, formatInputField, messageAfterTargetChange, structuredInputSchema, validateWorkflowInput } from "./workflowInput";

const tripSchema = {
    type: "object",
    properties: {
        destination: { type: "string" },
        needsHotel: { type: "boolean" },
        nights: { type: "integer" },
        notes: { type: "string" },
    },
    required: ["destination", "needsHotel"],
};

describe("structuredInputSchema", () => {
    it("returns a schema that names fields", () => {
        expect(structuredInputSchema(tripSchema)).toBe(tripSchema);
    });

    it("returns a schema that only lists required names", () => {
        expect(structuredInputSchema({ required: ["a"] })).toBeDefined();
    });

    it.each([
        ["every field optional", { properties: { a: { type: "string" } } }],
        ["text the only required field", { properties: { text: { type: "string" }, language: { type: "string" } }, required: ["text"] }],
    ])("leaves free text to a schema with %s, which the workflow runs on {text: ...}", (_label, schema) => {
        expect(structuredInputSchema(schema)).toBeUndefined();
    });

    it("keeps a schema whose text wrapper would be refused", () => {
        const closed = { properties: { a: { type: "string" } }, additionalProperties: false };
        expect(structuredInputSchema(closed)).toBe(closed);
    });

    it.each([undefined, null, "x", [], {}, { type: "object" }, { type: "object", properties: {} }])("treats %j as taking anything", schema => {
        expect(structuredInputSchema(schema)).toBeUndefined();
    });
});

describe("describeInputShape", () => {
    it("splits required from optional, optional sorted, null left out of the types", () => {
        const shape = describeInputShape({
            properties: { b: { type: ["string", "null"] }, a: { type: "number" }, id: { type: "string" } },
            required: ["id"],
        });
        expect(shape.required.map(formatInputField)).toEqual(["id (string)"]);
        expect(shape.optional.map(formatInputField)).toEqual(["a (number)", "b (string)"]);
    });

    it("names an untyped field without a type", () => {
        expect(describeInputShape({ properties: { x: {} }, required: ["x"] }).required.map(formatInputField)).toEqual(["x"]);
    });

    it("joins several accepted types", () => {
        expect(formatInputField({ name: "v", types: ["string", "integer"] })).toBe("v (string or integer)");
    });

    it("ignores a required entry that is not a string", () => {
        expect(describeInputShape({ properties: {}, required: ["ok", 3] }).required.map(f => f.name)).toEqual(["ok"]);
    });
});

describe("buildInputTemplate", () => {
    it("fills each required field with a value of its type and leaves optional ones out", () => {
        expect(JSON.parse(buildInputTemplate(tripSchema))).toEqual({ destination: "", needsHotel: false });
    });

    it("covers every JSON type, preferring default, const, then the first enum value", () => {
        const template = JSON.parse(
            buildInputTemplate({
                properties: {
                    count: { type: "number" },
                    whole: { type: "integer" },
                    flag: { type: "boolean" },
                    list: { type: "array" },
                    nested: { type: "object", properties: { id: { type: "string" }, extra: { type: "string" } }, required: ["id"] },
                    inferredObject: { properties: { id: { type: "integer" } }, required: ["id"] },
                    withDefault: { type: "string", default: "x" },
                    withConst: { const: 7 },
                    withEnum: { enum: ["a", "b"] },
                    nullable: { type: ["null", "boolean"] },
                    untyped: {},
                    opaque: "not a schema",
                },
                required: ["count", "whole", "flag", "list", "nested", "inferredObject", "withDefault", "withConst", "withEnum", "nullable", "untyped", "opaque"],
            })
        );
        expect(template).toEqual({
            count: 0,
            whole: 0,
            flag: false,
            list: [],
            nested: { id: "" },
            inferredObject: { id: 0 },
            withDefault: "x",
            withConst: 7,
            withEnum: "a",
            nullable: false,
            untyped: "",
            opaque: "",
        });
    });

    it("is pretty-printed so the user can edit it", () => {
        expect(buildInputTemplate(tripSchema)).toBe('{\n  "destination": "",\n  "needsHotel": false\n}');
    });

    it("yields a template that passes its own validation", () => {
        expect(validateWorkflowInput(buildInputTemplate(tripSchema), tripSchema)).toBeNull();
    });
});

describe("messageAfterTargetChange", () => {
    const otherSchema = { properties: { id: { type: "integer" } }, required: ["id"] };

    it("starts an empty form from the next workflow's template", () => {
        expect(JSON.parse(messageAfterTargetChange("  ", undefined, tripSchema))).toEqual({ destination: "", needsHotel: false });
    });

    it("replaces the previous workflow's untouched template", () => {
        expect(JSON.parse(messageAfterTargetChange(buildInputTemplate(tripSchema), tripSchema, otherSchema))).toEqual({ id: 0 });
    });

    it("clears an untouched template when the next target takes no structured input", () => {
        expect(messageAfterTargetChange(buildInputTemplate(tripSchema), tripSchema, undefined)).toBe("");
    });

    it("keeps text the user wrote", () => {
        expect(messageAfterTargetChange("my own words", tripSchema, otherSchema)).toBe("my own words");
        expect(messageAfterTargetChange("my own words", undefined, otherSchema)).toBe("my own words");
    });
});

describe("validateWorkflowInput", () => {
    it("accepts an object with the required fields", () => {
        expect(validateWorkflowInput('{"destination":"Porto","needsHotel":true,"nights":3}', tripSchema)).toBeNull();
    });

    it("accepts fields the schema does not declare, which the workflow decides about", () => {
        expect(validateWorkflowInput('{"destination":"Porto","needsHotel":true,"extra":1}', tripSchema)).toBeNull();
    });

    it("tells prose the shape to send", () => {
        const message = validateWorkflowInput("Plan me a trip to Porto", tripSchema);
        expect(message).toContain("enter a JSON object");
        expect(message).toContain("Required: destination (string), needsHotel (boolean).");
    });

    it("rejects JSON that is not an object", () => {
        for (const text of ["[1,2]", '"hi"', "null"]) {
            expect(validateWorkflowInput(text, tripSchema)).toContain("enter a JSON object.");
        }
    });

    it("says what is wrong with an object that does not parse", () => {
        expect(validateWorkflowInput('{"destination":"Porto",}', tripSchema)).toMatch(/enter a JSON object \(.+\)\. Required: destination/);
    });

    it("names every missing field at once", () => {
        expect(validateWorkflowInput("{}", tripSchema)).toBe('The input does not fit this workflow: missing "destination", "needsHotel".');
    });

    it("names a mistyped field next to a missing one", () => {
        expect(validateWorkflowInput('{"needsHotel":"yes"}', tripSchema)).toBe('The input does not fit this workflow: missing "destination"; "needsHotel" must be boolean, not string.');
    });

    it("tells integer from number and null from object", () => {
        expect(validateWorkflowInput('{"destination":"x","needsHotel":true,"nights":1.5}', tripSchema)).toContain('"nights" must be integer, not number');
        expect(validateWorkflowInput('{"o":null}', { properties: { o: { type: "object" } }, required: ["o"] })).toContain('"o" must be object, not null');
    });

    it("accepts any of several declared types, including null", () => {
        const schema = { properties: { v: { type: ["string", "null"] } }, required: ["v"] };
        expect(validateWorkflowInput('{"v":null}', schema)).toBeNull();
        expect(validateWorkflowInput('{"v":"a"}', schema)).toBeNull();
        expect(validateWorkflowInput('{"v":1}', schema)).toContain('"v" must be string or null, not number');
    });

    it("checks enum membership, comparing objects whatever their key order", () => {
        const schema = { properties: { size: { enum: ["s", "m"] }, point: { enum: [{ x: 1, y: 2 }] } }, required: ["size", "point"] };
        expect(validateWorkflowInput('{"size":"m","point":{"y":2,"x":1}}', schema)).toBeNull();
        expect(validateWorkflowInput('{"size":"xl","point":{"x":1,"y":2}}', schema)).toContain('"size" must be one of "s", "m"');
        expect(validateWorkflowInput('{"size":"s","point":{"x":2,"y":2}}', schema)).toContain('"point" must be one of {"x":1,"y":2}');
    });

    it("leaves a property with an unknown or missing type unchecked", () => {
        expect(validateWorkflowInput('{"a":1,"b":"x"}', { properties: { a: { type: "weird" }, b: {} }, required: ["a", "b"] })).toBeNull();
    });

    it("counts only own properties as present", () => {
        const schema = { properties: { destination: {}, constructor: {} }, required: ["destination", "constructor"] };
        expect(validateWorkflowInput('{"destination":"x"}', schema)).toContain('missing "constructor"');
    });

    it("refuses a field a closed schema does not declare", () => {
        const closed = { properties: { a: { type: "string" } }, required: ["a"], additionalProperties: false };
        expect(validateWorkflowInput('{"a":"x","typo":1}', closed)).toContain('"typo" is not a field of this workflow');
    });

    describe("a schema the text wrapper satisfies", () => {
        const textSchema = { properties: { text: { type: "string" }, language: { enum: ["en", "fr"] } }, required: ["text"] };

        it("accepts prose, which the workflow runs as {text: ...}", () => {
            expect(validateWorkflowInput("Plan me a trip to Porto", textSchema)).toBeNull();
        });

        it("accepts a JSON object that is not the input document, which the workflow also runs as text", () => {
            expect(validateWorkflowInput('{"language":"de"}', textSchema)).toBeNull();
            expect(validateWorkflowInput('{"topik":"x"}', { properties: { topic: { type: "string" } } })).toBeNull();
        });

        it("accepts an object that fits as the input document", () => {
            expect(validateWorkflowInput('{"text":"hello","language":"fr"}', textSchema)).toBeNull();
        });
    });

    it("omits the required list from the hint when the schema requires nothing", () => {
        const closed = { properties: { a: { type: "string" } }, additionalProperties: false };
        expect(validateWorkflowInput("prose", closed)).toBe("This workflow takes structured input: enter a JSON object.");
    });
});
