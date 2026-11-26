// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { matchScope, scopeHasPartialGlob } from "./scopeMatcher";
import { instanceScope, SCOPES } from "./scopes";

const FEEDBACK_READ_VERB = "read_feedback";
const TRACE_READ_VERB = "read_trace";

export interface FeedbackKindReach {
    all: boolean;
    subjectIds: readonly string[];
}

export interface FeedbackReach {
    agent: FeedbackKindReach;
    workflow: FeedbackKindReach;
}

function resolveKindReach(granted: readonly string[], category: "agent" | "workflow", wildcardScope: string, verb: string = FEEDBACK_READ_VERB): FeedbackKindReach {
    const seen = new Set<string>();
    const subjectIds: string[] = [];
    for (const scope of granted) {
        if (scopeHasPartialGlob(scope)) continue;
        if (matchScope(scope, wildcardScope)) {
            return { all: true, subjectIds: [] };
        }
        const parts = scope.split(":");
        if (parts.length !== 3) continue;
        const id = parts[1];
        if (id === "" || id === "_") continue;
        if (parts[0] !== category) continue;
        if (!matchScope(scope, instanceScope(category, id, verb))) continue;
        if (!seen.has(id)) {
            seen.add(id);
            subjectIds.push(id);
        }
    }
    return { all: false, subjectIds };
}

export function resolveFeedbackReach(granted: readonly string[]): FeedbackReach {
    return {
        agent: resolveKindReach(granted, "agent", SCOPES.agent.readFeedback),
        workflow: resolveKindReach(granted, "workflow", SCOPES.workflow.readFeedback),
    };
}

export function hasAnyFeedbackReadReach(granted: readonly string[]): boolean {
    const { agent, workflow } = resolveFeedbackReach(granted);
    return agent.all || agent.subjectIds.length > 0 || workflow.all || workflow.subjectIds.length > 0;
}

export function hasAnyTraceReadReach(granted: readonly string[]): boolean {
    const reach = resolveKindReach(granted, "agent", SCOPES.agent.readTrace, TRACE_READ_VERB);
    return reach.all || reach.subjectIds.length > 0;
}
