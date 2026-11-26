// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function extractNodeReferences(expression: string): string[] {
    if (!expression) return [];

    const nodeRefs = new Set<string>();

    const templateRegex = /\{\{([^}]+)\}\}/g;
    let templateMatch;

    while ((templateMatch = templateRegex.exec(expression)) !== null) {
        const content = templateMatch[1];

        const nodeRefRegex = /\b([a-zA-Z_][a-zA-Z0-9_]*)\s*\.\s*(output|input|[a-zA-Z_][a-zA-Z0-9_]*)/g;
        let refMatch;

        while ((refMatch = nodeRefRegex.exec(content)) !== null) {
            const nodeId = refMatch[1];

            if (nodeId === "workflow") {
                nodeRefs.add("__start__");
            } else {
                nodeRefs.add(nodeId);
            }
        }
    }

    return Array.from(nodeRefs);
}

export function validateNodeReferences(nodeReferences: string[], knownNodeIds: Set<string>): string[] {
    return nodeReferences.filter(nodeId => knownNodeIds.has(nodeId));
}

export function getValidNodeReferences(expression: string, knownNodeIds: Set<string>): string[] {
    const refs = extractNodeReferences(expression);
    return validateNodeReferences(refs, knownNodeIds);
}

export function extractNodeIdsFromConfig(config: { nodes?: Array<{ id?: string }> }): Set<string> {
    const ids = new Set<string>();

    ids.add("__start__");
    ids.add("__end__");

    if (config.nodes) {
        for (const node of config.nodes) {
            if (node.id) {
                ids.add(node.id);
            }
        }
    }

    return ids;
}
