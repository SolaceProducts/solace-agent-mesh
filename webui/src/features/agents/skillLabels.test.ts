// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { Skill } from "@/types";

import { builtinSkillRefLabel, customSkillLabel } from "./skillLabels";

const skill = (over: Partial<Skill>): Skill => ({
    id: "id",
    name: "name",
    description: "",
    tags: null,
    origin: "platform",
    discoveryStatus: "ready",
    hasReferences: false,
    hasAssets: false,
    hasTools: false,
    resourceCount: 0,
    referenceCount: 0,
    assetCount: 0,
    createdBy: "",
    updatedBy: "",
    createdAt: "",
    updatedAt: "",
    ...over,
});

describe("builtinSkillRefLabel", () => {
    test("prefers the catalog label", () => {
        expect(builtinSkillRefLabel("sam-docs", skill({ name: "sam-docs", label: "Solace Agent Mesh Documentation" }))).toBe("Solace Agent Mesh Documentation");
    });

    test("falls back to name when no label", () => {
        expect(builtinSkillRefLabel("sam-docs", skill({ name: "sam-docs" }))).toBe("sam-docs");
    });

    test("humanizes an unresolved ref (strips sam- and title-cases)", () => {
        expect(builtinSkillRefLabel("sam-build-redirect")).toBe("Build Redirect");
        expect(builtinSkillRefLabel("image-tools")).toBe("Image Tools");
    });

    test("falls back to the raw ref when humanization yields an empty string", () => {
        expect(builtinSkillRefLabel("sam-")).toBe("sam-");
        expect(builtinSkillRefLabel("-")).toBe("-");
    });
});

describe("customSkillLabel", () => {
    test("uses the skill name", () => {
        expect(customSkillLabel("uuid-1", skill({ id: "uuid-1", name: "My Skill" }))).toBe("My Skill");
    });

    test("falls back to the id when the skill is missing", () => {
        expect(customSkillLabel("uuid-1", undefined)).toBe("uuid-1");
    });
});
