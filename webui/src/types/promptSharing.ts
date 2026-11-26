// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type PromptShareAccessLevel = "resource_viewer" | "resource_editor";

export interface PromptShareResponse {
    id: string;
    userEmail: string;
    accessLevel: PromptShareAccessLevel;
    sharedByEmail: string;
    createdAt: string;
    updatedAt: string;
}

export interface BatchPromptShareRequest {
    shares: {
        userEmail: string;
        accessLevel: PromptShareAccessLevel;
    }[];
}

export interface BatchPromptShareResponse {
    created: PromptShareResponse[];
    updated: PromptShareResponse[];
    totalProcessed: number;
}
