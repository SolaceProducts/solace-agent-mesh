// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type CapabilityStatus = "unavailable" | "stable" | "beta" | "early_access" | "experimental";

export interface CapabilityInfo {
    key: string;
    name: string;
    description: string;
    status: CapabilityStatus;
    platform: string;
    hasEnvOverride: boolean;
}
