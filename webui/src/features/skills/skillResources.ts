// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Skill } from "@/types";

export function bundledToolName(tool: unknown): string {
    if (typeof tool === "string") return tool;
    if (tool && typeof tool === "object") {
        const rec = tool as Record<string, unknown>;
        const name = rec.tool_name ?? rec.name;
        if (typeof name === "string") return name;
    }
    return "";
}

export function skillToolNames(skill: Pick<Skill, "bundledTools">): string[] {
    return (skill.bundledTools ?? []).map(bundledToolName).filter(name => name !== "");
}

export function skillToolCount(skill: Pick<Skill, "bundledTools">): number {
    return skill.bundledTools?.length ?? 0;
}

export function skillResourceCount(skill: Pick<Skill, "resourceCount">): number {
    return skill.resourceCount ?? 0;
}

export function skillReferenceCount(skill: Pick<Skill, "referenceCount">): number {
    return skill.referenceCount ?? 0;
}

export function skillAssetCount(skill: Pick<Skill, "assetCount">): number {
    return skill.assetCount ?? 0;
}
