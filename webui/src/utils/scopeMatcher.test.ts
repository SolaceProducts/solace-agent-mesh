// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { hasScope, isValidScope, matchScope } from "./scopeMatcher";

describe("matchScope (parity with internal/auth/scope.go)", () => {
    const cases: ReadonlyArray<[name: string, granted: string, required: string, want: boolean]> = [
        ["wildcard matches anything", "*", "tool:file:read", true],
        ["wildcard matches simple", "*", "anything", true],

        ["exact match", "tool:file:read", "tool:file:read", true],
        ["exact mismatch", "tool:file:read", "tool:file:update", false],

        ["wildcard segment middle", "agent:*:invoke", "agent:my_agent:invoke", true],
        ["wildcard segment first", "*:file:read", "tool:file:read", true],
        ["wildcard segment mismatch", "agent:*:invoke", "agent:my_agent:read", false],

        ["suffix wildcard", "tool:basic:*", "tool:basic:read", true],
        ["suffix wildcard write", "tool:basic:*", "tool:basic:update", true],
        ["suffix wildcard deep", "tool:*:*", "tool:basic:read", true],
        ["suffix wildcard no match prefix", "tool:advanced:*", "tool:basic:read", false],

        ["shorter granted no wildcard", "tool:file", "tool:file:read", false],
        ["longer granted no wildcard", "tool:file:read:extra", "tool:file:read", false],

        ["multiple wildcards", "*:*:*", "agent:foo:invoke", true],
        ["wildcard with mismatch", "*:*:delegate", "agent:foo:invoke", false],

        ["empty granted", "", "tool:file:read", false],
        ["both empty", "", "", true],
        ["single segment exact", "admin", "admin", true],
        ["single segment mismatch", "admin", "user", false],

        ["monitor namespace wildcard", "monitor/namespace/*:a2a_messages:subscribe", "monitor/namespace/my-ns:a2a_messages:subscribe", true],

        ["user scope exact match", "agent:alice__my_agent:invoke", "agent:alice__my_agent:invoke", true],
        ["user scope wildcard", "agent:alice__*:invoke", "agent:alice__my_agent:invoke", true],
        ["user scope wrong user", "agent:bob__*:invoke", "agent:alice__my_agent:invoke", false],
        ["user scope universal grant", "agent:*:invoke", "agent:alice__my_agent:invoke", true],

        ["mid-segment wildcard *:*:read", "*:*:read", "agent_builder:foo:read", true],
        ["mid-segment wildcard agent_builder:*:read", "agent_builder:*:read", "agent_builder:foo:read", true],
        ["nested wildcard tool/ns/*:read", "tool/ns/*:read", "tool/ns/my-ns:read", true],

        ["held collection sentinel satisfies wildcard verb", "connector:_:*", "connector:*:read", true],
        ["held collection sentinel satisfies category wildcard", "connector:_:*", "connector:*:*", true],
        ["held collection sentinel satisfies same verb", "connector:_:read", "connector:*:read", true],
        ["held wildcard satisfies collection sentinel", "connector:*:read", "connector:_:read", true],
        ["flat category sentinel satisfies wildcard verb", "evaluation:_:*", "evaluation:*:read", true],
        ["equivalence does not cross categories", "connector:_:*", "skill:*:read", false],

        ["segment 3 sentinel is not a wildcard", "tool:email:_", "tool:email:send", false],
        ["sentinel verb satisfies nothing real", "connector:_:_", "connector:*:read", false],
        ["four segments are left alone", "x:_:y:z", "x:*:y:z", false],
        ["two-segment grant keeps prefix-wildcard reach", "rbac:*", "rbac:_:create", true],
        ["segment-2 glob is not the sentinel", "tool:*__search_users:invoke", "tool:*__search_users:invoke", true],
    ];

    test.each(cases)("%s", (_name, granted, required, want) => {
        expect(matchScope(granted, required)).toBe(want);
    });

    test("agent_builder full wildcard subsumes a concrete verb", () => {
        expect(matchScope("agent_builder:*:*", "agent_builder:my_agent:update")).toBe(true);
    });
    test("per-resource wildcard subsumes one verb but not another resource", () => {
        expect(matchScope("agent_builder:my_agent:*", "agent_builder:my_agent:delete")).toBe(true);
        expect(matchScope("agent_builder:my_agent:*", "agent_builder:other:delete")).toBe(false);
    });
    test("read-only-on-all wildcard", () => {
        expect(matchScope("agent_builder:*:read", "agent_builder:any_agent:read")).toBe(true);
        expect(matchScope("agent_builder:*:read", "agent_builder:any_agent:update")).toBe(false);
    });
    test("a coarse category wildcard covers the collection-create scope (_ is a literal to the matcher)", () => {
        expect(matchScope("agent_builder:*:*", "agent_builder:_:create")).toBe(true);
    });
});

describe("hasScope", () => {
    const granted = ["rbac:_:read", "agent_builder:*:*"];

    test("true when any granted scope satisfies the requirement", () => {
        expect(hasScope(granted, "rbac:_:read")).toBe(true);
        expect(hasScope(granted, "agent_builder:my_agent:deploy")).toBe(true);
    });
    test("false when no granted scope satisfies the requirement", () => {
        expect(hasScope(granted, "evaluation:_:read")).toBe(false);
        expect(hasScope(granted, "agent:my_agent:invoke")).toBe(false);
    });
    test("superadmin grants everything", () => {
        expect(hasScope(["*:*:*"], "rbac:_:delete")).toBe(true);
        expect(hasScope(["*"], "anything:at:all")).toBe(true);
    });
    test("empty granted satisfies nothing", () => {
        expect(hasScope([], "rbac:_:read")).toBe(false);
    });
});

describe("isValidScope (parity with internal/rbac/scope.go Pattern)", () => {
    const valid = ["tool:file:read", "agent_builder:_:create", "*", "tool:*:read", "a", "a-b", "a_b", "a0", "agent:Orchestrator:invoke", "connector:hr-bot:read"];
    test.each(valid)("valid: %s", scope => {
        expect(isValidScope(scope)).toBe(true);
    });

    const invalid = ["", "UPPERCASE", "has space", "has.dot", ":leading-colon", "trailing-colon:", "double::colon"];
    test.each(invalid)("invalid: %s", scope => {
        expect(isValidScope(scope)).toBe(false);
    });
});
