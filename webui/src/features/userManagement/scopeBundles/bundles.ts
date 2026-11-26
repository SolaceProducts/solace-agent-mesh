// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { anyInstanceScope, collectionScope, composeScope, instanceScope, LEGACY_COLLECTION_SEGMENT } from "@/utils/scopes";

export interface ComponentBundle {
    key: string;
    label: string;
    description: string;
    scopes: string[];
}

export const COMPONENT_CORE_SCOPES: string[] = [
    anyInstanceScope("agent_builder", "*"),
    anyInstanceScope("workflow_builder", "*"),
    composeScope("tool", "*", "*"),
    anyInstanceScope("agent", "invoke"),
    anyInstanceScope("workflow", "invoke"),
    anyInstanceScope("connector", "read"),
    anyInstanceScope("skill", "read"),
    anyInstanceScope("toolset", "read"),
    anyInstanceScope("model_config", "read"),
];

const writeScopes = (category: string): string[] => [collectionScope(category, "create"), anyInstanceScope(category, "update"), anyInstanceScope(category, "delete")];

export const OPTIONAL_COMPONENT_BUNDLES: readonly ComponentBundle[] = [
    { key: "connectors", label: "Connectors", description: "Create, edit, and delete all connectors.", scopes: writeScopes("connector") },
    { key: "skills", label: "Skills", description: "Create, edit, and delete all skills.", scopes: writeScopes("skill") },
    { key: "toolsets", label: "Toolsets", description: "Create, edit, and delete all toolsets.", scopes: writeScopes("toolset") },
    { key: "models", label: "Models", description: "Create, edit, and delete all model configurations.", scopes: writeScopes("model_config") },
];

const OPTIONAL_COMPONENT_SCOPES: string[] = OPTIONAL_COMPONENT_BUNDLES.flatMap(b => b.scopes);

export const COMPONENT_SCOPES: string[] = [...COMPONENT_CORE_SCOPES, ...OPTIONAL_COMPONENT_SCOPES];

export const ENTRYPOINT_SCOPES: string[] = [anyInstanceScope("entrypoint", "*")];

export const EVALUATION_SCOPES: { createRun: string[]; view: string[] } = {
    createRun: [collectionScope("evaluation", "*")],
    view: [collectionScope("evaluation", "read")],
};

export type ComponentKind = "agent" | "workflow";

export interface ComponentOption {
    id: string;
    name: string;
    description?: string;
}

export interface ScopeBundleEditorProps {
    value: string[];
    onChange: (scopes: string[]) => void;
    agents?: readonly ComponentOption[];
    workflows?: readonly ComponentOption[];
    disabled?: boolean;
}

const invokeScope = (kind: ComponentKind, id: string) => instanceScope(kind, id, "invoke");

export function componentIds(scopes: readonly string[], kind: ComponentKind): string[] {
    const ids: string[] = [];
    for (const scope of scopes) {
        const [category, resource, verb] = scope.split(":");
        if (category === kind && verb === "invoke" && resource !== "*") ids.push(resource);
    }
    return ids;
}

export function hasAllComponents(scopes: readonly string[], kind: ComponentKind): boolean {
    return scopes.includes(invokeScope(kind, "*"));
}

export function addComponent(scopes: readonly string[], kind: ComponentKind, id: string): string[] {
    const scope = invokeScope(kind, id);
    return scopes.includes(scope) ? [...scopes] : [scope, ...scopes];
}

export function removeComponent(scopes: readonly string[], kind: ComponentKind, id: string): string[] {
    const scope = invokeScope(kind, id);
    return scopes.filter(s => s !== scope);
}

export function grantAllComponents(scopes: readonly string[], kind: ComponentKind): string[] {
    const stripped = scopes.filter(s => {
        const [category, , verb] = s.split(":");
        return !(category === kind && verb === "invoke");
    });
    return [...stripped, invokeScope(kind, "*")];
}

export const ALL_ID = "*";

export type CardGrant = "feedbackRead" | "traceRead";

export interface CardGrants {
    invoke: boolean;
    feedbackRead: boolean;
    traceRead: boolean;
}

const wildcardOr = (kind: ComponentKind, id: string, verb: string) => (id === ALL_ID ? anyInstanceScope(kind, verb) : instanceScope(kind, id, verb));
const cardInvokeScope = (kind: ComponentKind, id: string) => wildcardOr(kind, id, "invoke");
const feedbackReadScope = (kind: ComponentKind, id: string) => wildcardOr(kind, id, "read_feedback");
const traceReadScope = (id: string) => wildcardOr("agent", id, "read_trace");

const splitScope = (scope: string): [string, string, string] => {
    const [category = "", resource = "", verb = ""] = scope.split(":");
    return [category, resource, verb];
};

const cardVerbs = (kind: ComponentKind): readonly string[] => (kind === "agent" ? ["invoke", "read_feedback", "read_trace"] : ["invoke", "read_feedback"]);

export function cardIds(scopes: readonly string[], kind: ComponentKind): string[] {
    const ids: string[] = [];
    for (const scope of scopes) {
        const [category, resource, verb] = splitScope(scope);
        if (category !== kind || !cardVerbs(kind).includes(verb)) continue;
        if (resource === ALL_ID || resource === "" || resource === LEGACY_COLLECTION_SEGMENT || resource.includes("*")) continue;
        if (!ids.includes(resource)) ids.push(resource);
    }
    return ids;
}

export function hasAllCard(scopes: readonly string[], kind: ComponentKind): boolean {
    return cardVerbs(kind).some(verb => scopes.includes(anyInstanceScope(kind, verb)));
}

export function cardGrants(scopes: readonly string[], kind: ComponentKind, id: string): CardGrants {
    const invoke = scopes.includes(cardInvokeScope(kind, id));
    const traceRead = kind === "agent" && scopes.includes(traceReadScope(id));
    const feedbackRead = scopes.includes(feedbackReadScope(kind, id));
    return { invoke, feedbackRead, traceRead };
}

const without = (scopes: readonly string[], drop: readonly string[]) => scopes.filter(s => !drop.includes(s));
const withAppended = (scopes: readonly string[], add: string) => (scopes.includes(add) ? [...scopes] : [...scopes, add]);

export function setCardGrant(scopes: readonly string[], kind: ComponentKind, id: string, grant: CardGrant, on: boolean): string[] {
    if (grant === "traceRead" && kind !== "agent") return [...scopes];
    const scope = grant === "traceRead" ? traceReadScope(id) : feedbackReadScope(kind, id);
    return on ? withAppended(scopes, scope) : without(scopes, [scope]);
}

export const addCard = addComponent;

export function addAllCard(scopes: readonly string[], kind: ComponentKind): string[] {
    let out: readonly string[] = scopes;
    for (const id of cardIds(scopes, kind)) out = removeCard(out, kind, id);
    return withAppended(out, anyInstanceScope(kind, "invoke"));
}

export function removeCard(scopes: readonly string[], kind: ComponentKind, id: string): string[] {
    let out = without(
        scopes,
        cardVerbs(kind).map(verb => wildcardOr(kind, id, verb))
    );
    if (id === ALL_ID) {
        for (const perID of cardIds(out, kind)) out = removeCard(out, kind, perID);
    }
    return out;
}

export function ensureInvokeForCards(scopes: readonly string[]): string[] {
    let out = [...scopes];
    for (const kind of ["agent", "workflow"] as const) {
        if (hasAllCard(out, kind)) {
            out = withAppended(out, anyInstanceScope(kind, "invoke"));
            continue;
        }
        for (const id of cardIds(out, kind)) out = withAppended(out, instanceScope(kind, id, "invoke"));
    }
    return out;
}
