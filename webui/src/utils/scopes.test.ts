// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { hasScope } from "./scopeMatcher";
import { anyInstanceScope, canReadSpend, collectionScope, composeScope, instanceScope, isSuperAdmin, SCOPES, SUPERADMIN_SCOPE } from "./scopes";

describe("scope builders", () => {
    test("composeScope joins three raw segments with ':'", () => {
        expect(composeScope("rbac", "_", "read")).toBe("rbac:_:read");
    });

    test("collectionScope emits <category>:*:<verb>", () => {
        expect(collectionScope("rbac", "read")).toBe("rbac:*:read");
        expect(collectionScope("evaluation", "invoke")).toBe("evaluation:*:invoke");
    });

    test("instanceScope emits <category>:<name>:<verb>", () => {
        expect(instanceScope("agent_builder", "my_agent", "update")).toBe("agent_builder:my_agent:update");
    });

    test("anyInstanceScope wildcards the instance segment", () => {
        expect(anyInstanceScope("agent_builder", "read")).toBe("agent_builder:*:read");
        expect(anyInstanceScope("agent", "invoke")).toBe("agent:*:invoke");
    });
});

describe("SCOPES vocabulary (exact strings)", () => {
    test("SUPERADMIN_SCOPE is the three-segment wildcard", () => {
        expect(SUPERADMIN_SCOPE).toBe("*:*:*");
    });

    test("every SCOPES entry matches its expected literal", () => {
        expect(SCOPES).toEqual({
            rbac: {
                read: "rbac:*:read",
            },
            evaluation: {
                read: "evaluation:*:read",
                create: "evaluation:*:create",
                update: "evaluation:*:update",
                delete: "evaluation:*:delete",
                invoke: "evaluation:*:invoke",
            },
            tool: {
                read: "tool:*:read",
            },
            deployment: {
                read: "deployment:*:read",
            },
            analytics: {
                read: "analytics:*:read",
                read_content: "analytics:*:read_content",
            },
            activity: {
                readAny: "activity:*:read",
            },
            usage: {
                readAll: "*:*:read_usage",
            },
            profile_provider: {
                read: "profile_provider:*:read",
                update: "profile_provider:*:update",
            },
            builder: {
                use: "builder:*:use",
            },
            notify: {
                send: "notify:*:send",
                send_others: "notify:*:send_others",
                read_others: "notify:*:read_others",
            },
            prompt: {
                share: "prompt:*:share",
            },
            agent_builder: {
                read: "agent_builder:*:read",
                create: "agent_builder:*:create",
                update: "agent_builder:*:update",
                delete: "agent_builder:*:delete",
                deploy: "agent_builder:*:deploy",
                test: "agent_builder:*:test",
            },
            connector: {
                read: "connector:*:read",
                create: "connector:*:create",
                update: "connector:*:update",
                delete: "connector:*:delete",
            },
            toolset: {
                read: "toolset:*:read",
                create: "toolset:*:create",
                update: "toolset:*:update",
                delete: "toolset:*:delete",
            },
            webui_settings: {
                read: "webui_settings:*:read",
                update: "webui_settings:*:update",
            },
            budget: {
                read: "budget:*:read",
                create: "budget:*:create",
                update: "budget:*:update",
                delete: "budget:*:delete",
                assign: "budget:*:assign",
            },
            workflow_builder: {
                read: "workflow_builder:*:read",
                create: "workflow_builder:*:create",
                update: "workflow_builder:*:update",
                delete: "workflow_builder:*:delete",
                deploy: "workflow_builder:*:deploy",
            },
            skill: {
                read: "skill:*:read",
                create: "skill:*:create",
                update: "skill:*:update",
                delete: "skill:*:delete",
            },
            entrypoint: {
                read: "entrypoint:*:read",
                create: "entrypoint:*:create",
                update: "entrypoint:*:update",
                delete: "entrypoint:*:delete",
                deploy: "entrypoint:*:deploy",
            },
            model_config: {
                read: "model_config:*:read",
                create: "model_config:*:create",
                update: "model_config:*:update",
                delete: "model_config:*:delete",
            },
            agent: {
                readFeedback: "agent:*:read_feedback",
                readTrace: "agent:*:read_trace",
            },
            workflow: {
                readFeedback: "workflow:*:read_feedback",
            },
        });
    });
});

describe("vocabulary ↔ matcher agreement", () => {
    const allScopes = Object.values(SCOPES).flatMap(group => Object.values(group));

    test("superadmin satisfies every declared scope", () => {
        for (const required of allScopes) {
            expect(hasScope([SUPERADMIN_SCOPE], required)).toBe(true);
        }
    });

    test("a scope satisfies itself and nothing it shouldn't", () => {
        expect(hasScope([SCOPES.rbac.read], SCOPES.rbac.read)).toBe(true);
        expect(hasScope([SCOPES.rbac.read], SCOPES.evaluation.delete)).toBe(false);
        expect(hasScope([], SCOPES.rbac.read)).toBe(false);
    });

    test("a role saved with the older _ spelling still satisfies the * vocabulary", () => {
        expect(hasScope(["rbac:_:read"], SCOPES.rbac.read)).toBe(true);
        expect(hasScope(["evaluation:_:*"], SCOPES.evaluation.invoke)).toBe(true);
    });
});

describe("isSuperAdmin: only a universal wildcard, never a mere rbac reader", () => {
    test("is true for the universal wildcards", () => {
        expect(isSuperAdmin([SUPERADMIN_SCOPE])).toBe(true);
        expect(isSuperAdmin(["*"])).toBe(true);
        expect(isSuperAdmin(["rbac:_:read", "*"])).toBe(true);
    });

    test("is false for any enumerated scope set, no matter how broad", () => {
        expect(isSuperAdmin([])).toBe(false);
        expect(isSuperAdmin([SCOPES.rbac.read])).toBe(false);
        expect(isSuperAdmin([SCOPES.rbac.read, SCOPES.evaluation.read, SCOPES.evaluation.create, SCOPES.evaluation.update, SCOPES.evaluation.delete])).toBe(false);
        expect(isSuperAdmin(["rbac:*:*"])).toBe(false);
    });
});

describe("canReadSpend", () => {
    test.each([
        ["the super admin wildcard", [SUPERADMIN_SCOPE]],
        ["the auth-off identity's bare *", ["*"]],
        ["*:*:read_usage itself", [SCOPES.usage.readAll]],
    ])("is true for %s", (_what, scopes) => {
        expect(canReadSpend(scopes)).toBe(true);
    });

    test.each([
        ["no scopes", []],
        ["a budget reader", [SCOPES.budget.read]],
        ["one category's usage scope", ["agent:*:read_usage"]],
        ["a partial-glob resource", ["**:*:read_usage"]],
        ["a partial-glob category", ["a*:*:read_usage"]],
    ])("is false for %s", (_what, scopes) => {
        expect(canReadSpend(scopes)).toBe(false);
    });
});
