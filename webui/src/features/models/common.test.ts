// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { ModelConfig } from "@/api/models/types";

import { getDisplayModelName, isModelConfigured, modelConfigurationIssues } from "./common";

function model(fields: Partial<ModelConfig>): ModelConfig {
    return { id: "m1", alias: "general", provider: "openai", modelName: "gpt-4o", apiBase: null, authType: null, authConfig: {}, modelParams: {}, maxInputTokens: null, description: null, createdBy: "u", ...fields } as ModelConfig;
}

describe("isModelConfigured", () => {
    test.each([
        { provider: "openai", modelName: "gpt-4o", configured: true },
        { provider: null, modelName: null, configured: false },
        { provider: "openai", modelName: null, configured: false },
        { provider: "openai", modelName: "", configured: false },
        { provider: "openai", modelName: " \t ", configured: false },
    ])("provider $provider with model name $modelName → $configured", ({ provider, modelName, configured }) => {
        expect(isModelConfigured(model({ provider, modelName }))).toBe(configured);
    });
});

describe("modelConfigurationIssues", () => {
    test("empty for a healthy model with no stored apiBase", () => {
        expect(modelConfigurationIssues(model({ apiBase: null }))).toEqual([]);
    });

    test("empty for a healthy model with a well-formed apiBase", () => {
        expect(modelConfigurationIssues(model({ apiBase: "https://api.example.com" }))).toEqual([]);
    });

    test("flags the scheme-no-slashes typo on the stored apiBase", () => {
        const issues = modelConfigurationIssues(model({ apiBase: "https:example.com" }));
        expect(issues).toHaveLength(1);
        expect(issues[0]).toContain("https:example.com");
    });

    test("flags a bare-hostname apiBase", () => {
        expect(modelConfigurationIssues(model({ apiBase: "example.com" }))).toHaveLength(1);
    });
});

describe("getDisplayModelName", () => {
    test.each([
        { modelName: "openai/gpt-4o", modelType: "chat", shown: "gpt-4o" },
        { modelName: "anthropic/claude-sonnet-4-6", modelType: "chat", shown: "claude-sonnet-4-6" },
        { modelName: "openai/meta-llama/Llama-3-70b", modelType: "chat", shown: "meta-llama/Llama-3-70b" },
        { modelName: "meta-llama/Llama-3-70b", modelType: "chat", shown: "meta-llama/Llama-3-70b" },
        { modelName: "hosted_vllm/llama", modelType: "chat", shown: "hosted_vllm/llama" },
        { modelName: "openai/", modelType: "chat", shown: "openai/" },
        { modelName: "claude-sonnet-4-6", modelType: undefined, shown: "claude-sonnet-4-6" },
        { modelName: "openai/gpt-6-luna-decisions", modelType: "decision", shown: "openai/gpt-6-luna-decisions" },
        { modelName: null, modelType: "chat", shown: "" },
    ] as const)("$modelName ($modelType) shows as $shown", ({ modelName, modelType, shown }) => {
        expect(getDisplayModelName(model({ modelName, modelType }))).toBe(shown);
    });
});
