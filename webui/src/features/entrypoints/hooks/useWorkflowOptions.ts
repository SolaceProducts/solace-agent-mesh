// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";

export interface WorkflowOption {
    name: string;
    displayName: string;
}

export interface WorkflowOptions {
    options: WorkflowOption[];
    isError: boolean;
}

const EMPTY: never[] = [];

export const useWorkflowOptions = (): WorkflowOptions => {
    const { data: cards = EMPTY, isError } = useAgentCards();

    const options = useMemo(
        () =>
            (cards ?? EMPTY)
                .filter(card => card.isWorkflow && !card.isEphemeral)
                .map(card => ({ name: card.name, displayName: card.displayName || card.name }))
                .sort((a, b) => a.displayName.localeCompare(b.displayName)),
        [cards]
    );

    return { options, isError };
};

export const pinWorkflowValue = (options: WorkflowOption[], value: string | undefined | null): WorkflowOption[] => {
    if (!value) return options;
    if (options.some(o => o.name === value)) return options;
    return [...options, { name: value, displayName: value }];
};
