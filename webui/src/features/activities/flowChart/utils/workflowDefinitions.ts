// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { getWorkflowConfig } from "@/utils/agentUtils";

import type { AgentCardInfo } from "@/types";

import type { WorkflowDefinitions } from "./types";

export function workflowDefinitionsOf(cards: AgentCardInfo[] | undefined): WorkflowDefinitions {
    const definitions: WorkflowDefinitions = {};
    for (const card of cards ?? []) {
        const nodes = getWorkflowConfig(card)?.nodes;
        if (card.name && nodes) definitions[card.name] = nodes;
    }
    return definitions;
}
