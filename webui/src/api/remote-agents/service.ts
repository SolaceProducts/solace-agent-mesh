// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface FetchAgentCardRequest {
    agentCardUrl?: string;
    authenticationType?: string;
    authConfig: Record<string, string | undefined> | null;
    customHeaders: Record<string, string> | null;
}
