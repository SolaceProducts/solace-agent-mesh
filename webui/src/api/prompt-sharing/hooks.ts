// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { BatchPromptShareRequest } from "@/types/promptSharing";

import { promptShareKeys } from "./keys";
import { createPromptShares, getPromptShares, replacePromptShares } from "./service";

export function usePromptShares(promptGroupId: string, options?: { enabled?: boolean }) {
    return useQuery({
        queryKey: promptShareKeys.forGroup(promptGroupId),
        queryFn: () => getPromptShares(promptGroupId),
        enabled: !!promptGroupId && (options?.enabled ?? true),
    });
}

export function useCreatePromptShares() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ promptGroupId, body }: { promptGroupId: string; body: BatchPromptShareRequest }) => createPromptShares(promptGroupId, body),
        onSuccess: (_, { promptGroupId }) => {
            queryClient.invalidateQueries({ queryKey: promptShareKeys.forGroup(promptGroupId) });
        },
    });
}

export function useReplacePromptShares() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ promptGroupId, body }: { promptGroupId: string; body: BatchPromptShareRequest }) => replacePromptShares(promptGroupId, body),
        onSuccess: (_, { promptGroupId }) => {
            queryClient.invalidateQueries({ queryKey: promptShareKeys.forGroup(promptGroupId) });
        },
    });
}
