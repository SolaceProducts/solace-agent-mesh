// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const WORKFLOW_VIEW_BASE_PATH = "/agent-mesh/workflows";

const PARENT_SEPARATOR = ",";

export function buildWorkflowNavigationUrl(targetWorkflow: string, parentPath: string[] = []): string {
    const encodedTarget = encodeURIComponent(targetWorkflow);
    if (parentPath.length === 0) {
        return `${WORKFLOW_VIEW_BASE_PATH}/${encodedTarget}`;
    }
    const search = new URLSearchParams({ from: parentPath.map(encodeURIComponent).join(PARENT_SEPARATOR) });
    return `${WORKFLOW_VIEW_BASE_PATH}/${encodedTarget}?${search}`;
}

export function parseWorkflowParentPath(fromParam: string | null): string[] {
    if (!fromParam) return [];
    return fromParam.split(PARENT_SEPARATOR).map(decodeParentName);
}

function decodeParentName(segment: string): string {
    try {
        return decodeURIComponent(segment);
    } catch {
        return segment;
    }
}
