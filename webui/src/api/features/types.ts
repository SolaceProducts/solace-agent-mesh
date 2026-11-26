// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface FeatureFlagInfo {
    key: string;
    name: string;
    releasePhase: string;
    resolved: boolean;
    hasEnvOverride: boolean;
    registryDefault: boolean;
    description: string;
}
