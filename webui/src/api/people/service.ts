// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import type { PeopleSearchResponse } from "@/types";

export const searchPeople = async (query: string, limit: number = 10): Promise<PeopleSearchResponse> => {
    const response = await api.webui.get<PeopleSearchResponse>(`/api/v1/people/search?q=${encodeURIComponent(query)}&pageSize=${limit}`);
    return response;
};
