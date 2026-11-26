// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const NON_OPERABLE_NODE_TYPES = new Set(["loading", "column-header", "workflow-container", "workflow-bg", "wf-end", "wf-tool", "wf-workflow", "wf-switch", "wf-decision", "wf-map", "wf-loop", "wf-condition"]);

export function isKeyboardOperableNodeType(type: string): boolean {
    return !NON_OPERABLE_NODE_TYPES.has(type);
}
