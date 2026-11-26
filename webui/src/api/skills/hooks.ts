// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { Skill } from "@/types";

import { skillKeys } from "./keys";
import { type CreateSkillFromConfigRequest, type SavedSkill, saveSkillFromConfig } from "./service";
import { skillsApi } from "./versioned";

const SKILL_PROCESSING_STATUSES = new Set<Skill["discoveryStatus"]>(["created", "pending"]);
const PROCESSING_REFETCH_MS = 5_000;
const EVICT_FILE_CONTENT_ON_INACTIVE_MS = 0;

export function isSkillProcessing(status?: Skill["discoveryStatus"]): boolean {
    return !!status && SKILL_PROCESSING_STATUSES.has(status);
}

export function useSkills({ enabled = true }: { enabled?: boolean } = {}) {
    return useQuery({
        queryKey: skillKeys.list(),
        queryFn: skillsApi.getAll,
        refetchOnMount: "always",
        refetchInterval: query => (query.state.data?.some(skill => isSkillProcessing(skill.discoveryStatus)) ? PROCESSING_REFETCH_MS : false),
        retry: 0,
        enabled,
    });
}

export function useBuiltinSkills({ enabled = true }: { enabled?: boolean } = {}) {
    return useQuery({
        queryKey: skillKeys.builtins(),
        queryFn: skillsApi.getBuiltin,
        retry: 0,
        enabled,
    });
}

export function useSkill(id: string) {
    return useQuery({
        queryKey: skillKeys.detail(id),
        queryFn: () => skillsApi.get(id),
        enabled: !!id,
        refetchInterval: query => (isSkillProcessing(query.state.data?.discoveryStatus) ? PROCESSING_REFETCH_MS : false),
        retry: 0,
    });
}

export function useSkillFiles(id: string) {
    return useQuery({
        queryKey: skillKeys.files(id),
        queryFn: () => skillsApi.getFiles(id),
        enabled: !!id,
        retry: 0,
    });
}

export function useSkillFileContent(id: string, path: string | null) {
    return useQuery({
        queryKey: skillKeys.fileContent(id, path ?? ""),
        queryFn: () => skillsApi.getFileContent(id, path!),
        enabled: !!id && !!path,
        retry: 0,
        gcTime: EVICT_FILE_CONTENT_ON_INACTIVE_MS,
    });
}

export function useSaveSkillFromConfigMutation() {
    const queryClient = useQueryClient();
    return useMutation<SavedSkill, Error, CreateSkillFromConfigRequest>({
        mutationFn: async request => {
            const saved = await saveSkillFromConfig(skillsApi, request);
            if (saved.action !== "replace_deferred") return saved;
            const upload = saved.commit;
            return {
                ...saved,
                commit: async () => {
                    try {
                        await upload();
                    } finally {
                        queryClient.invalidateQueries({ queryKey: skillKeys.all });
                    }
                },
            };
        },
        onSuccess: saved => {
            queryClient.invalidateQueries({ queryKey: saved.action === "updated" ? skillKeys.all : skillKeys.lists() });
        },
    });
}
