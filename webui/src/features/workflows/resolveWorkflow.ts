// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Workflow } from "@/types/workflow";

import type { AgentCardInfo } from "@/types";

const WIRE_NAME_PREFIX = "workflow_";

function workflowIdFromWireName(name: string): string | undefined {
    if (!name.startsWith(WIRE_NAME_PREFIX)) return undefined;
    return name.slice(WIRE_NAME_PREFIX.length).replaceAll("_", "-");
}

export function resolveWorkflowByName(workflows: Workflow[], name: string): Workflow | undefined {
    const byName = workflows.find(w => w.name === name);
    if (byName) return byName;
    const id = workflowIdFromWireName(name);
    return id ? workflows.find(w => w.id === id) : undefined;
}

export function resolvePlatformWorkflow(workflows: Workflow[], card: AgentCardInfo): Workflow | undefined {
    return resolveWorkflowByName(workflows, card.name);
}
