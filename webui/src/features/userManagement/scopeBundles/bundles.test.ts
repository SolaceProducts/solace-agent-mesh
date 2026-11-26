// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import {
    addAllCard,
    addCard,
    addComponent,
    ALL_ID,
    cardGrants,
    cardIds,
    COMPONENT_CORE_SCOPES,
    COMPONENT_SCOPES,
    componentIds,
    ensureInvokeForCards,
    ENTRYPOINT_SCOPES,
    EVALUATION_SCOPES,
    grantAllComponents,
    hasAllCard,
    hasAllComponents,
    OPTIONAL_COMPONENT_BUNDLES,
    removeCard,
    removeComponent,
    setCardGrant,
} from "./bundles";
import { EXAMPLE_ROLE_SCOPES } from "./fixtures";

describe("bundle scope constants", () => {
    test("Components core emits the canonical 3-segment grammar verbatim", () => {
        expect(COMPONENT_CORE_SCOPES).toEqual(["agent_builder:*:*", "workflow_builder:*:*", "tool:*:*", "agent:*:invoke", "workflow:*:invoke", "connector:*:read", "skill:*:read", "toolset:*:read", "model_config:*:read"]);
    });

    test("optional sub-components layer the write verbs on the core read", () => {
        expect(OPTIONAL_COMPONENT_BUNDLES.map(b => b.scopes)).toEqual([
            ["connector:*:create", "connector:*:update", "connector:*:delete"],
            ["skill:*:create", "skill:*:update", "skill:*:delete"],
            ["toolset:*:create", "toolset:*:update", "toolset:*:delete"],
            ["model_config:*:create", "model_config:*:update", "model_config:*:delete"],
        ]);
    });

    test("COMPONENT_SCOPES is the core plus every optional add-on", () => {
        expect(COMPONENT_SCOPES).toEqual([...COMPONENT_CORE_SCOPES, ...OPTIONAL_COMPONENT_BUNDLES.flatMap(b => b.scopes)]);
    });

    test("Entrypoints and Evaluations scopes", () => {
        expect(ENTRYPOINT_SCOPES).toEqual(["entrypoint:*:*"]);
        expect(EVALUATION_SCOPES.createRun).toEqual(["evaluation:*:*"]);
        expect(EVALUATION_SCOPES.view).toEqual(["evaluation:*:read"]);
    });
});

describe("per-instance grants", () => {
    const agentScope = "agent:agent_1:invoke";

    test("componentIds excludes the wildcard", () => {
        expect(componentIds([agentScope, "agent:*:invoke"], "agent")).toEqual(["agent_1"]);
        expect(componentIds([agentScope], "workflow")).toEqual([]);
    });

    test("addComponent / removeComponent", () => {
        expect(addComponent([], "agent", "agent_1")).toEqual([agentScope]);
        expect(addComponent([agentScope], "agent", "agent_1")).toEqual([agentScope]);
        expect(removeComponent([agentScope], "agent", "agent_1")).toEqual([]);
    });

    test("addComponent prepends the new instance so it sorts to the top", () => {
        expect(addComponent([agentScope], "agent", "agent_2")).toEqual(["agent:agent_2:invoke", agentScope]);
    });

    test("wildcard via id '*' and hasAllComponents", () => {
        const all = addComponent([], "agent", "*");
        expect(all).toEqual(["agent:*:invoke"]);
        expect(hasAllComponents(all, "agent")).toBe(true);
        expect(hasAllComponents([], "agent")).toBe(false);
    });
});

describe("grantAllComponents", () => {
    test("removes only its own kind's per-instance grants, keeps the other kind and unrelated scopes", () => {
        const value = ["agent:agent_1:invoke", "agent:agent_2:invoke", "workflow:wf_1:invoke", "entrypoint:*:*"];

        const result = grantAllComponents(value, "agent");

        expect(result).toContain("agent:*:invoke");
        expect(result).not.toContain("agent:agent_1:invoke");
        expect(result).not.toContain("agent:agent_2:invoke");
        expect(result).toContain("workflow:wf_1:invoke");
        expect(result).toContain("entrypoint:*:*");
    });

    test("collapses an existing wildcard rather than duplicating it", () => {
        const result = grantAllComponents(["agent:*:invoke", "agent:agent_1:invoke"], "agent");
        expect(result.filter(s => s === "agent:*:invoke")).toHaveLength(1);
        expect(result).not.toContain("agent:agent_1:invoke");
    });
});

describe("feedback grant cards", () => {
    const inv = (kind: string, id: string) => `${kind}:${id}:invoke`;
    const fb = (kind: string, id: string) => `${kind}:${id}:read_feedback`;
    const tr = (id: string) => `agent:${id}:read_trace`;

    test("cardIds unions invoke, read_feedback and read_trace for agents", () => {
        expect(cardIds([inv("agent", "a"), fb("agent", "b"), tr("c")], "agent")).toEqual(["a", "b", "c"]);
    });

    test("cardIds unions invoke and read_feedback for workflows and ignores agent grants", () => {
        expect(cardIds([inv("workflow", "w"), fb("workflow", "x"), inv("agent", "a"), fb("agent", "b"), tr("c")], "workflow")).toEqual(["w", "x"]);
    });

    test("the collection sentinel and globbed ids are not cards in either kind", () => {
        expect(cardIds(["agent:_:read_feedback", "agent:hr-*:read_feedback", "agent:*:read_trace", "agent:_:invoke"], "agent")).toEqual([]);
        expect(cardIds(["workflow:_:read_feedback", "workflow:on-*:read_feedback", "workflow:*:invoke"], "workflow")).toEqual([]);
    });

    test("hasAllCard reads only its own kind's wildcards", () => {
        expect(hasAllCard(["agent:*:invoke"], "agent")).toBe(true);
        expect(hasAllCard(["agent:*:read_feedback"], "agent")).toBe(true);
        expect(hasAllCard(["agent:*:read_trace"], "agent")).toBe(true);
        expect(hasAllCard(["agent:*:read_feedback", "agent:*:read_trace", "agent:*:invoke"], "workflow")).toBe(false);
        expect(hasAllCard(["workflow:*:read_feedback"], "workflow")).toBe(true);
        expect(hasAllCard(["workflow:*:read_feedback"], "agent")).toBe(false);
    });

    test("cardGrants reads per kind, each box reporting only the scope it stands for, and traces is always false for workflows", () => {
        expect(cardGrants([fb("agent", "a")], "agent", "a")).toEqual({ invoke: false, feedbackRead: true, traceRead: false });
        expect(cardGrants([tr("a")], "agent", "a")).toEqual({ invoke: false, feedbackRead: false, traceRead: true });
        expect(cardGrants([fb("workflow", "w")], "workflow", "w")).toEqual({ invoke: false, feedbackRead: true, traceRead: false });
        expect(cardGrants([fb("agent", "w")], "workflow", "w")).toEqual({ invoke: false, feedbackRead: false, traceRead: false });
    });

    test("cardGrants reports invoke per kind, including the All card reading the wildcard", () => {
        expect(cardGrants([inv("agent", "a")], "agent", "a")).toEqual({ invoke: true, feedbackRead: false, traceRead: false });
        expect(cardGrants(["agent:*:invoke"], "agent", ALL_ID)).toEqual({ invoke: true, feedbackRead: false, traceRead: false });
        expect(cardGrants([], "agent", ALL_ID)).toEqual({ invoke: false, feedbackRead: false, traceRead: false });
    });

    test("cardGrants invoke does not leak across kinds", () => {
        expect(cardGrants([inv("agent", "w")], "workflow", "w")).toEqual({ invoke: false, feedbackRead: false, traceRead: false });
    });

    test("setCardGrant on agents writes feedback and traces independently", () => {
        expect(setCardGrant([inv("agent", "a")], "agent", "a", "traceRead", true)).toEqual([inv("agent", "a"), tr("a")]);
        expect(setCardGrant([inv("agent", "a"), fb("agent", "a"), tr("a")], "agent", "a", "feedbackRead", false)).toEqual([inv("agent", "a"), tr("a")]);
        expect(setCardGrant([inv("agent", "a"), fb("agent", "a"), tr("a")], "agent", "a", "traceRead", false)).toEqual([inv("agent", "a"), fb("agent", "a")]);
    });

    test("setCardGrant on workflows writes workflow feedback and never a trace", () => {
        expect(setCardGrant([inv("workflow", "w")], "workflow", "w", "feedbackRead", true)).toEqual([inv("workflow", "w"), fb("workflow", "w")]);
        const before = [inv("workflow", "w")];
        expect(setCardGrant(before, "workflow", "w", "traceRead", true)).toEqual(before);
    });

    test("setCardGrant on an All card emits that kind's wildcard only", () => {
        expect(setCardGrant(["agent:*:invoke"], "agent", ALL_ID, "traceRead", true)).toEqual(["agent:*:invoke", "agent:*:read_trace"]);
        expect(setCardGrant(["workflow:*:invoke"], "workflow", ALL_ID, "feedbackRead", true)).toEqual(["workflow:*:invoke", "workflow:*:read_feedback"]);
    });

    test("ticking feedback on All Agents leaves All Workflows untouched", () => {
        const scopes = setCardGrant(["agent:*:invoke", "workflow:*:invoke"], "agent", ALL_ID, "feedbackRead", true);
        expect(cardGrants(scopes, "workflow", ALL_ID)).toEqual({ invoke: true, feedbackRead: false, traceRead: false });
    });

    test("addCard grants invoke only", () => {
        expect(addCard([], "agent", "a")).toEqual([inv("agent", "a")]);
    });

    test("addAllCard replaces every specific card of its kind and leaves the other kind alone", () => {
        const value = [inv("agent", "a"), fb("agent", "a"), tr("a"), inv("agent", "b"), inv("workflow", "w"), fb("workflow", "w")];
        expect(addAllCard(value, "agent")).toEqual([inv("workflow", "w"), fb("workflow", "w"), "agent:*:invoke"]);
    });

    test("addAllCard is a no-op once All already covers the kind", () => {
        expect(addAllCard(["agent:*:invoke"], "agent")).toEqual(["agent:*:invoke"]);
    });

    test("removeCard strips the kind's verbs for one id and nothing else", () => {
        const value = [inv("agent", "a"), fb("agent", "a"), tr("a"), inv("agent", "b"), fb("workflow", "a")];
        expect(removeCard(value, "agent", "a")).toEqual([inv("agent", "b"), fb("workflow", "a")]);
    });

    test("removing one All card leaves the other kind's wildcards intact", () => {
        const both = ["agent:*:invoke", "agent:*:read_feedback", "agent:*:read_trace", "workflow:*:invoke", "workflow:*:read_feedback"];
        expect(removeCard(both, "agent", ALL_ID)).toEqual(["workflow:*:invoke", "workflow:*:read_feedback"]);
        expect(removeCard(both, "workflow", ALL_ID)).toEqual(["agent:*:invoke", "agent:*:read_feedback", "agent:*:read_trace"]);
    });

    test("ensureInvokeForCards adds invoke per kind for every displayed card, idempotently", () => {
        const value = [fb("agent", "a"), fb("workflow", "w")];
        const result = ensureInvokeForCards(value);
        expect(result).toEqual(expect.arrayContaining([...value, inv("agent", "a"), inv("workflow", "w")]));
        expect(result).not.toContain("workflow:*:invoke");
        expect(result).not.toContain("agent:*:invoke");
        expect(ensureInvokeForCards(result)).toEqual(result);
        expect(ensureInvokeForCards([])).toEqual([]);
    });

    test("ensureInvokeForCards adds invoke for an id it has never seen, since the kind is in the scope", () => {
        expect(ensureInvokeForCards([fb("agent", "ghost")])).toEqual([fb("agent", "ghost"), inv("agent", "ghost")]);
    });

    test("ensureInvokeForCards writes only the wildcard when an All card covers a kind, skipping the per-id grants it shadows", () => {
        const value = ["agent:*:read_feedback", fb("agent", "hr")];
        const result = ensureInvokeForCards(value);
        expect(result).toEqual([...value, "agent:*:invoke"]);
        expect(result).not.toContain(inv("agent", "hr"));
    });

    test("the example roles round-trip as exact scope sets", () => {
        const allInvoke = ["agent:*:invoke", "workflow:*:invoke"];
        let reviewer = setCardGrant(allInvoke, "agent", ALL_ID, "feedbackRead", true);
        reviewer = setCardGrant(reviewer, "agent", ALL_ID, "traceRead", true);
        reviewer = setCardGrant(reviewer, "workflow", ALL_ID, "feedbackRead", true);
        expect(reviewer).toEqual(EXAMPLE_ROLE_SCOPES.generalReviewer);
        expect(cardGrants(reviewer, "agent", ALL_ID)).toEqual({ invoke: true, feedbackRead: true, traceRead: true });
        expect(cardGrants(reviewer, "workflow", ALL_ID)).toEqual({ invoke: true, feedbackRead: true, traceRead: false });
    });

    describe("removing the All card", () => {
        test("also clears the per-id grants the All card was hiding", () => {
            const loaded = ["agent:*:read_feedback", fb("agent", "hr"), tr("hr"), inv("agent", "hr")];
            expect(removeCard(loaded, "agent", ALL_ID)).toEqual([]);
        });

        test("leaves the other kind untouched", () => {
            const loaded = ["agent:*:read_feedback", fb("agent", "hr"), fb("workflow", "w"), inv("workflow", "w")];
            expect(removeCard(loaded, "agent", ALL_ID)).toEqual([fb("workflow", "w"), inv("workflow", "w")]);
        });

        test("removing one per-id card still leaves its siblings", () => {
            const loaded = [fb("agent", "hr"), tr("hr"), fb("agent", "fin")];
            expect(removeCard(loaded, "agent", "hr")).toEqual([fb("agent", "fin")]);
        });
    });
});
