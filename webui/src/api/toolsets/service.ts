// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface CreateToolsetRequest {
    name: string;
    description?: string;
}

export type UpdateToolsetRequest = CreateToolsetRequest;

export interface ToolsetBuildTarget {
    os: string;
    arch: string;
    source: string;
    pythonVersion?: string;
}
