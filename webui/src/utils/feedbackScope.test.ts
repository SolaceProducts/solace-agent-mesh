// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { hasAnyFeedbackReadReach, hasAnyTraceReadReach, resolveFeedbackReach } from "./feedbackScope";

const agentFeedbackScope = (id: string): string => `agent:${id}:read_feedback`;
const workflowFeedbackScope = (id: string): string => `workflow:${id}:read_feedback`;

describe("resolveFeedbackReach", () => {
    test("an agent wildcard gives agent reach and not workflow reach", () => {
        const reach = resolveFeedbackReach(["agent:*:read_feedback"]);
        expect(reach.agent).toEqual({ all: true, subjectIds: [] });
        expect(reach.workflow).toEqual({ all: false, subjectIds: [] });
    });

    test("a workflow wildcard gives workflow reach and not agent reach", () => {
        const reach = resolveFeedbackReach(["workflow:*:read_feedback"]);
        expect(reach.workflow).toEqual({ all: true, subjectIds: [] });
        expect(reach.agent).toEqual({ all: false, subjectIds: [] });
    });

    test("a bare superadmin star gives both kinds reach", () => {
        const reach = resolveFeedbackReach(["*"]);
        expect(reach.agent).toEqual({ all: true, subjectIds: [] });
        expect(reach.workflow).toEqual({ all: true, subjectIds: [] });
    });

    test("per-subject grants of each kind land in their own kind's list, first-seen and deduplicated", () => {
        const reach = resolveFeedbackReach([agentFeedbackScope("a"), workflowFeedbackScope("w1"), agentFeedbackScope("b"), agentFeedbackScope("a"), workflowFeedbackScope("w1")]);
        expect(reach.agent).toEqual({ all: false, subjectIds: ["a", "b"] });
        expect(reach.workflow).toEqual({ all: false, subjectIds: ["w1"] });
    });

    test("a trace grant alone gives no reach — a trace grant opens nothing feedback-side", () => {
        const reach = resolveFeedbackReach(["agent:hr-bot:read_trace"]);
        expect(reach.agent).toEqual({ all: false, subjectIds: [] });
        expect(reach.workflow).toEqual({ all: false, subjectIds: [] });
    });

    test("a verb wildcard on a concrete subject still reduces to per-subject reach, in the right kind", () => {
        const reach = resolveFeedbackReach(["agent:hr-bot:*", "workflow:billing-flow:*"]);
        expect(reach.agent).toEqual({ all: false, subjectIds: ["hr-bot"] });
        expect(reach.workflow).toEqual({ all: false, subjectIds: ["billing-flow"] });
    });

    test.each([
        ["globbed id", agentFeedbackScope("hr-*")],
        ["collection sentinel canonicalizes to the wildcard, not a literal id", agentFeedbackScope("_")],
        ["empty id segment", agentFeedbackScope("")],
        ["wrong segment count (too few)", "agent:read_feedback"],
        ["wrong segment count (too many)", "agent:hr-bot:read_feedback:extra"],
    ])("%s drops for the agent kind, except the sentinel which is the wildcard", (desc, scope) => {
        const reach = resolveFeedbackReach([scope]);
        if (desc === "collection sentinel canonicalizes to the wildcard, not a literal id") {
            expect(reach.agent).toEqual({ all: true, subjectIds: [] });
        } else {
            expect(reach.agent).toEqual({ all: false, subjectIds: [] });
        }
        expect(reach.workflow).toEqual({ all: false, subjectIds: [] });
    });

    test("the dead feedback:*:read and feedback:<id>:read scopes give no reach in either kind", () => {
        const reach = resolveFeedbackReach(["feedback:*:read", "feedback:hr-bot:read"]);
        expect(reach.agent).toEqual({ all: false, subjectIds: [] });
        expect(reach.workflow).toEqual({ all: false, subjectIds: [] });
    });

    test("empty grant list yields deny for both kinds", () => {
        const reach = resolveFeedbackReach([]);
        expect(reach.agent).toEqual({ all: false, subjectIds: [] });
        expect(reach.workflow).toEqual({ all: false, subjectIds: [] });
    });

    test("unrelated scopes yield deny", () => {
        const reach = resolveFeedbackReach(["analytics:_:read", "agent:hr-bot:invoke", "feedback:x:share"]);
        expect(reach.agent).toEqual({ all: false, subjectIds: [] });
        expect(reach.workflow).toEqual({ all: false, subjectIds: [] });
    });
});

describe("hasAnyFeedbackReadReach", () => {
    test("true for agent reach alone", () => {
        expect(hasAnyFeedbackReadReach([agentFeedbackScope("hr-bot")])).toBe(true);
    });

    test("true for workflow reach alone", () => {
        expect(hasAnyFeedbackReadReach([workflowFeedbackScope("billing-flow")])).toBe(true);
    });

    test("false for no feedback-read reach at all", () => {
        expect(hasAnyFeedbackReadReach([])).toBe(false);
        expect(hasAnyFeedbackReadReach(["agent:hr-bot:invoke"])).toBe(false);
    });

    test("false for a trace-only grant — the case most likely to be got wrong", () => {
        expect(hasAnyFeedbackReadReach(["agent:hr-bot:read_trace"])).toBe(false);
    });
});

describe("parity with the backend on partial globs", () => {
    const none = { all: false, subjectIds: [] };

    test.each([
        ["*:hr-bot:read_feedback", "a globbed kind must not reach either kind"],
        ["agent:**:read_feedback", "`**` must not read as the tenant-wide wildcard"],
        ["a*:hr:read_feedback", "a partial glob in the kind must be dropped"],
        ["agent:?:read_feedback", "`?` in the subject must be dropped"],
        ["workflow:h?-bot:read_feedback", "`?` in a workflow subject must be dropped"],
    ])("drops %s — %s", (grant: string) => {
        expect(resolveFeedbackReach([grant])).toEqual({ agent: none, workflow: none });
    });

    test("keeps the shapes the backend keeps", () => {
        expect(resolveFeedbackReach(["agent:*:read_feedback"])).toEqual({
            agent: { all: true, subjectIds: [] },
            workflow: none,
        });
        expect(resolveFeedbackReach(["agent:hr-bot:read_feedback"])).toEqual({
            agent: { all: false, subjectIds: ["hr-bot"] },
            workflow: none,
        });
        expect(resolveFeedbackReach(["workflow:wf-1:read_feedback"])).toEqual({
            agent: none,
            workflow: { all: false, subjectIds: ["wf-1"] },
        });
        expect(resolveFeedbackReach(["agent:hr-bot:*"])).toEqual({
            agent: { all: false, subjectIds: ["hr-bot"] },
            workflow: none,
        });
    });

    test("a bare * in the kind segment is not a partial glob and still reaches both", () => {
        expect(resolveFeedbackReach(["*:*:read_feedback"])).toEqual({
            agent: { all: true, subjectIds: [] },
            workflow: { all: true, subjectIds: [] },
        });
    });

    test("the bare * still reaches every kind", () => {
        expect(resolveFeedbackReach(["*"])).toEqual({
            agent: { all: true, subjectIds: [] },
            workflow: { all: true, subjectIds: [] },
        });
    });
});

describe("hasAnyTraceReadReach", () => {
    test("true for the trace wildcard and for the grants that match it", () => {
        expect(hasAnyTraceReadReach(["agent:*:read_trace"])).toBe(true);
        expect(hasAnyTraceReadReach(["agent:*:*"])).toBe(true);
        expect(hasAnyTraceReadReach(["*:*:*"])).toBe(true);
        expect(hasAnyTraceReadReach(["*"])).toBe(true);
    });

    test("true for a per-agent trace grant, a verb wildcard on an agent, and the collection sentinel the server canonicalizes", () => {
        expect(hasAnyTraceReadReach(["agent:hr-bot:read_trace"])).toBe(true);
        expect(hasAnyTraceReadReach(["agent:hr-bot:*"])).toBe(true);
        expect(hasAnyTraceReadReach(["agent:_:read_trace"])).toBe(true);
    });

    test("false for feedback reads, invoke, a workflow grant or a partial glob", () => {
        expect(hasAnyTraceReadReach([])).toBe(false);
        expect(hasAnyTraceReadReach(["agent:*:read_feedback", "agent:hr-bot:read_feedback"])).toBe(false);
        expect(hasAnyTraceReadReach(["agent:*:invoke"])).toBe(false);
        expect(hasAnyTraceReadReach(["workflow:*:*"])).toBe(false);
        expect(hasAnyTraceReadReach(["agent:hr-*:read_trace"])).toBe(false);
    });
});
