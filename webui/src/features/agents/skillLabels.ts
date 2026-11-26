// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Skill } from "@/types";

const humanizeSkillRef = (ref: string): string => {
    const humanized = ref
        .replace(/^sam-/, "")
        .split(/[-_]/)
        .filter(Boolean)
        .map(word => word.charAt(0).toUpperCase() + word.slice(1))
        .join(" ");
    return humanized || ref;
};

export const builtinSkillRefLabel = (ref: string, builtin?: Skill): string => builtin?.label ?? builtin?.name ?? humanizeSkillRef(ref);

export const customSkillLabel = (id: string, skill?: Skill): string => skill?.label ?? skill?.name ?? id;
