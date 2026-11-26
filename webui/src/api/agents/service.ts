// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface CreateAgentRequest {
    [key: string]: unknown;
}

export type SavedAgent = { id?: string; deploymentStatus?: string; deployError?: string } | null;
