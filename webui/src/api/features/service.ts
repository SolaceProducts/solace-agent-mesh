// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";
import type { PaginatedResponse } from "@/api/common";

import type { FeatureFlagInfo } from "./types";

export async function fetchFeatureFlags(): Promise<FeatureFlagInfo[]> {
    const response = await api.webui.get("/api/v1/config/features", {
        credentials: "include",
        headers: { Accept: "application/json" },
        fullResponse: true,
    });
    if (!response.ok) {
        throw new Error(`Features endpoint returned ${response.status}`);
    }
    const body: PaginatedResponse<FeatureFlagInfo> = await response.json();
    return body.data;
}
