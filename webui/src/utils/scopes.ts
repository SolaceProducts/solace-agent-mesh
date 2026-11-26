// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { hasStrictScope } from "./scopeMatcher";

export const LEGACY_COLLECTION_SEGMENT = "_";

export const SUPERADMIN_SCOPE = "*:*:*";

export function composeScope(category: string, segment2: string, verb: string): string {
    return `${category}:${segment2}:${verb}`;
}

export function collectionScope(category: string, verb: string): string {
    return composeScope(category, "*", verb);
}

export function instanceScope(category: string, name: string, verb: string): string {
    return composeScope(category, name, verb);
}

export function anyInstanceScope(category: string, verb: string): string {
    return composeScope(category, "*", verb);
}

export const SCOPES = {
    rbac: {
        read: collectionScope("rbac", "read"),
    },
    evaluation: {
        read: collectionScope("evaluation", "read"),
        create: collectionScope("evaluation", "create"),
        update: collectionScope("evaluation", "update"),
        delete: collectionScope("evaluation", "delete"),
        invoke: collectionScope("evaluation", "invoke"),
    },
    tool: {
        read: collectionScope("tool", "read"),
    },
    deployment: {
        read: collectionScope("deployment", "read"),
    },
    analytics: {
        read: collectionScope("analytics", "read"),
        read_content: collectionScope("analytics", "read_content"),
    },
    activity: {
        readAny: anyInstanceScope("activity", "read"),
    },
    usage: {
        readAll: composeScope("*", "*", "read_usage"),
    },
    profile_provider: {
        read: anyInstanceScope("profile_provider", "read"),
        update: collectionScope("profile_provider", "update"),
    },
    webui_settings: {
        read: anyInstanceScope("webui_settings", "read"),
        update: anyInstanceScope("webui_settings", "update"),
    },
    budget: {
        read: anyInstanceScope("budget", "read"),
        create: anyInstanceScope("budget", "create"),
        update: anyInstanceScope("budget", "update"),
        delete: anyInstanceScope("budget", "delete"),
        assign: anyInstanceScope("budget", "assign"),
    },
    builder: {
        use: collectionScope("builder", "use"),
    },
    notify: {
        send: collectionScope("notify", "send"),
        send_others: collectionScope("notify", "send_others"),
        read_others: collectionScope("notify", "read_others"),
    },
    prompt: {
        share: collectionScope("prompt", "share"),
    },

    agent_builder: {
        read: anyInstanceScope("agent_builder", "read"),
        create: collectionScope("agent_builder", "create"),
        update: anyInstanceScope("agent_builder", "update"),
        delete: anyInstanceScope("agent_builder", "delete"),
        deploy: anyInstanceScope("agent_builder", "deploy"),
        test: anyInstanceScope("agent_builder", "test"),
    },
    connector: {
        read: anyInstanceScope("connector", "read"),
        create: collectionScope("connector", "create"),
        update: anyInstanceScope("connector", "update"),
        delete: anyInstanceScope("connector", "delete"),
    },
    toolset: {
        read: anyInstanceScope("toolset", "read"),
        create: collectionScope("toolset", "create"),
        update: anyInstanceScope("toolset", "update"),
        delete: anyInstanceScope("toolset", "delete"),
    },
    workflow_builder: {
        read: anyInstanceScope("workflow_builder", "read"),
        create: collectionScope("workflow_builder", "create"),
        update: anyInstanceScope("workflow_builder", "update"),
        delete: anyInstanceScope("workflow_builder", "delete"),
        deploy: anyInstanceScope("workflow_builder", "deploy"),
    },
    skill: {
        read: anyInstanceScope("skill", "read"),
        create: collectionScope("skill", "create"),
        update: anyInstanceScope("skill", "update"),
        delete: anyInstanceScope("skill", "delete"),
    },
    entrypoint: {
        read: anyInstanceScope("entrypoint", "read"),
        create: collectionScope("entrypoint", "create"),
        update: anyInstanceScope("entrypoint", "update"),
        delete: anyInstanceScope("entrypoint", "delete"),
        deploy: anyInstanceScope("entrypoint", "deploy"),
    },
    model_config: {
        read: anyInstanceScope("model_config", "read"),
        create: collectionScope("model_config", "create"),
        update: anyInstanceScope("model_config", "update"),
        delete: anyInstanceScope("model_config", "delete"),
    },
    agent: {
        readFeedback: anyInstanceScope("agent", "read_feedback"),
        readTrace: anyInstanceScope("agent", "read_trace"),
    },
    workflow: {
        readFeedback: anyInstanceScope("workflow", "read_feedback"),
    },
} as const;

export function isSuperAdmin(scopes: readonly string[]): boolean {
    return scopes.some(s => s === "*" || s === SUPERADMIN_SCOPE);
}

export function canReadSpend(scopes: readonly string[]): boolean {
    return hasStrictScope(scopes, SCOPES.usage.readAll);
}
