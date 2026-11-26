// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { SkillsApi } from "@/api/skills";

import type { Skill } from "@/types";

export interface SaveSkillData {
    description: string;
    file: File;
}

export async function saveSkill(skills: SkillsApi, data: SaveSkillData): Promise<Skill> {
    return skills.create(data.description, data.file);
}

export async function reuploadSkill(skills: SkillsApi, id: string, file: File): Promise<Skill> {
    return skills.uploadZip(id, file);
}
