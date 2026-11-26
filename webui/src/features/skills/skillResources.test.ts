// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { Skill } from "@/types";

import { bundledToolName, skillAssetCount, skillReferenceCount, skillResourceCount, skillToolCount, skillToolNames } from "./skillResources";

describe("bundledToolName", () => {
    test("reads the snake_case tool_name wire key", () => {
        expect(bundledToolName({ tool_name: "render_chart" })).toBe("render_chart");
    });
    test("falls back to name when tool_name is absent", () => {
        expect(bundledToolName({ name: "legacy_tool" })).toBe("legacy_tool");
    });
    test("prefers tool_name over name", () => {
        expect(bundledToolName({ tool_name: "canonical", name: "other" })).toBe("canonical");
    });
    test("accepts a bare string", () => {
        expect(bundledToolName("plain")).toBe("plain");
    });
    test("returns empty string for an unnamed or non-object entry", () => {
        expect(bundledToolName({})).toBe("");
        expect(bundledToolName(null)).toBe("");
        expect(bundledToolName(42)).toBe("");
    });
});

describe("skillToolNames", () => {
    test("maps entries to names and drops unnamed ones", () => {
        const skill = { bundledTools: [{ tool_name: "a" }, {}, { name: "b" }] } as unknown as Pick<Skill, "bundledTools">;
        expect(skillToolNames(skill)).toEqual(["a", "b"]);
    });
    test("returns [] when bundledTools is undefined", () => {
        expect(skillToolNames({} as Pick<Skill, "bundledTools">)).toEqual([]);
    });
});

describe("skillToolCount", () => {
    test("counts every entry, including unnamed ones", () => {
        expect(skillToolCount({ bundledTools: [{ tool_name: "a" }, {}] } as unknown as Pick<Skill, "bundledTools">)).toBe(2);
    });
    test("returns 0 when bundledTools is undefined", () => {
        expect(skillToolCount({} as Pick<Skill, "bundledTools">)).toBe(0);
    });
});

describe("skillResourceCount", () => {
    test("passes through resourceCount", () => {
        expect(skillResourceCount({ resourceCount: 7 })).toBe(7);
    });
    test("returns 0 when resourceCount is undefined", () => {
        expect(skillResourceCount({} as Pick<Skill, "resourceCount">)).toBe(0);
    });
});

describe("skillReferenceCount / skillAssetCount", () => {
    test("pass through their respective fields", () => {
        expect(skillReferenceCount({ referenceCount: 4 })).toBe(4);
        expect(skillAssetCount({ assetCount: 3 })).toBe(3);
    });
    test("return 0 when undefined", () => {
        expect(skillReferenceCount({} as Pick<Skill, "referenceCount">)).toBe(0);
        expect(skillAssetCount({} as Pick<Skill, "assetCount">)).toBe(0);
    });
});
