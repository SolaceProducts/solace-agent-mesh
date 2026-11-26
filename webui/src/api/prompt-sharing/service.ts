// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { PaginatedResponse } from "@/api/common";

import type { BatchPromptShareRequest, BatchPromptShareResponse, PromptShareResponse } from "@/types/promptSharing";

import { api } from "../client";

export const getPromptShares = async (promptGroupId: string) => {
    return api.webui.get<PaginatedResponse<PromptShareResponse>>(`/api/v1/prompts/groups/${promptGroupId}/shares`);
};

export const createPromptShares = async (promptGroupId: string, body: BatchPromptShareRequest) => {
    return api.webui.post<BatchPromptShareResponse>(`/api/v1/prompts/groups/${promptGroupId}/shares`, body);
};

export const replacePromptShares = async (promptGroupId: string, body: BatchPromptShareRequest): Promise<void> => {
    await api.webui.put(`/api/v1/prompts/groups/${promptGroupId}/shares`, body);
};
