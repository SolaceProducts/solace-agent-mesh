// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";
import { useWorkflowsQuery } from "@/api/workflows";

import { useUserContext } from "@/providers/contexts";

import type { DeploymentStatus } from "@/types/models";

import { resolvePlatformWorkflow } from "@/features/workflows/resolveWorkflow";

export interface WorkflowOption {
    name: string;
    displayName: string;
    deploymentStatus?: DeploymentStatus | "";
}

export interface WorkflowOptions {
    options: WorkflowOption[];
    readable: boolean;
    isLoading: boolean;
    isError: boolean;
}

const EMPTY: never[] = [];

export function useWorkflowOptions(enabled = true): WorkflowOptions {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const readable = userHasCapability(CAPABILITIES.readWorkflows);
    const requested = enabled && readable;
    const workflowsQuery = useWorkflowsQuery({ enabled: requested });
    const cardsQuery = useAgentCards();
    const workflows = workflowsQuery.data ?? EMPTY;
    const cards = cardsQuery.data ?? EMPTY;

    const options = useMemo(() => {
        const byName = new Map<string, WorkflowOption>();
        for (const wf of workflows) {
            byName.set(wf.name, { name: wf.name, displayName: wf.displayName || wf.name, deploymentStatus: wf.deploymentStatus });
        }
        for (const card of cards) {
            if (!card.isWorkflow || card.isEphemeral || byName.has(card.name) || resolvePlatformWorkflow(workflows, card)) continue;
            byName.set(card.name, { name: card.name, displayName: card.displayName || card.name });
        }
        return Array.from(byName.values()).sort((a, b) => a.displayName.localeCompare(b.displayName));
    }, [workflows, cards]);

    return {
        options,
        readable,
        isLoading: (requested && workflowsQuery.isLoading === true) || (enabled && cardsQuery.isLoading === true),
        isError: requested && workflowsQuery.isError === true,
    };
}
