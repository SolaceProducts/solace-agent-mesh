// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const WORKFLOW_ID_PARAM = "id";

export function workflowManagementUrl(id: string): string {
    return `/workflows?${WORKFLOW_ID_PARAM}=${encodeURIComponent(id)}`;
}
