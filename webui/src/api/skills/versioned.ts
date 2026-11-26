// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Skill, SkillFilesResponse } from "@/types";

import type { SkillFileContent } from "./service";
import { createSkillV2, deleteSkillV2, getBuiltinSkillsV2, getCustomSkillsV2, getSkillFileContentV2, getSkillFilesV2, getSkillV2, listBuiltinSkillsV2, listCustomSkillsV2, uploadSkillZipV2 } from "./v2";

export interface SkillsApi {
    getAll: () => Promise<Skill[]>;
    getBuiltin: () => Promise<Skill[]>;
    get: (id: string) => Promise<Skill>;
    create: (description: string, file: File) => Promise<Skill>;
    delete: (id: string) => Promise<void>;
    uploadZip: (id: string, file: File) => Promise<Skill>;
    getFiles: (id: string) => Promise<SkillFilesResponse>;
    getFileContent: (id: string, path: string) => Promise<SkillFileContent>;
    listCustom: () => Promise<Skill[]>;
    listBuiltin: () => Promise<Skill[]>;
}

export const skillsApi: SkillsApi = {
    getAll: getCustomSkillsV2,
    getBuiltin: getBuiltinSkillsV2,
    get: getSkillV2,
    create: createSkillV2,
    delete: deleteSkillV2,
    uploadZip: uploadSkillZipV2,
    getFiles: getSkillFilesV2,
    getFileContent: getSkillFileContentV2,
    listCustom: listCustomSkillsV2,
    listBuiltin: listBuiltinSkillsV2,
};
