// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { promptImportSchema } from "./prompt-import.schema";

const basePrompt = {
    name: "Imported Prompt",
    promptText: "You are imported.",
};

describe("promptImportSchema accepts exportedAt as either a Python epoch-ms number or a Go RFC3339 string", () => {
    it("accepts a Python epoch-ms number", () => {
        const result = promptImportSchema.safeParse({
            version: "1.0",
            exportedAt: 1783111183670,
            prompt: basePrompt,
        });
        expect(result.success).toBe(true);
    });

    it("accepts a Go RFC3339 string", () => {
        const result = promptImportSchema.safeParse({
            version: "1.0",
            exportedAt: "2026-07-03T20:39:43Z",
            prompt: basePrompt,
        });
        expect(result.success).toBe(true);
    });

    it("rejects an empty string", () => {
        const result = promptImportSchema.safeParse({
            version: "1.0",
            exportedAt: "",
            prompt: basePrompt,
        });
        expect(result.success).toBe(false);
    });

    it.each([0, -1, 1.5])("rejects non-positive/non-integer number %p", value => {
        const result = promptImportSchema.safeParse({
            version: "1.0",
            exportedAt: value,
            prompt: basePrompt,
        });
        expect(result.success).toBe(false);
    });

    it("accepts a full Python-shaped export with numeric metadata", () => {
        const result = promptImportSchema.safeParse({
            version: "1.0",
            exportedAt: 1783111183670,
            prompt: {
                ...basePrompt,
                description: "from python",
                category: "general",
                command: "py",
                metadata: {
                    authorName: "Ada Lovelace",
                    originalVersion: 3,
                    originalCreatedAt: 1783111183502,
                },
            },
        });
        expect(result.success).toBe(true);
    });
});
