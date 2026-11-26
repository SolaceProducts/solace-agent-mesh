// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { MessageFE } from "@/types";

import { activitySuppressionFor, borrowedStepsSource, isApprovedBuildInterrupted, resumeSurface } from "./buildInterruption";
import type { BuildManifest } from "./hooks/useBuilderManifest";

type Comp = NonNullable<BuildManifest["components"]>[number];

const draft: Comp = { filename: "a.yaml", type: "agent", name: "A", status: "draft", origin: "session" };
const manifestOf = (components: Comp[]): BuildManifest => ({ name: "build", components });

const base = { planApproved: true, isApplyingChanges: false, isResponding: false, isCancelling: false, turnEndedAbnormally: true, hasDeployFailure: false, isBuildActivating: false, manifest: manifestOf([draft]) };

describe("isApprovedBuildInterrupted", () => {
    test("an approved build whose last turn ended abnormally, with a component unfinished, is interrupted", () => {
        expect(isApprovedBuildInterrupted(base)).toBe(true);
    });

    test.each([
        ["the plan is not approved yet", { planApproved: false }],
        ["an Apply Changes edit is in flight", { isApplyingChanges: true }],
        ["the agent is responding", { isResponding: true }],
        ["a cancel is still being processed", { isCancelling: true }],
        ["the last turn ended normally", { turnEndedAbnormally: false }],
        ["a save or deploy failure stands", { hasDeployFailure: true }],
        ["a save or deploy is running", { isBuildActivating: true }],
        ["there is no manifest", { manifest: null }],
    ])("is not interrupted when %s", (_name, over) => {
        expect(isApprovedBuildInterrupted({ ...base, ...over })).toBe(false);
    });

    test.each(["validated", "deployed", "error", "blocked"])("is not interrupted once every component is %s: there is nothing left to resume", status => {
        expect(isApprovedBuildInterrupted({ ...base, manifest: manifestOf([{ ...draft, status }]) })).toBe(false);
    });

    test("one unfinished component among finished ones is enough", () => {
        const manifest = manifestOf([
            { ...draft, status: "validated" },
            { ...draft, name: "B", filename: "b.yaml" },
        ]);

        expect(isApprovedBuildInterrupted({ ...base, manifest })).toBe(true);
    });

    test("a session component with no origin counts as part of the build", () => {
        expect(isApprovedBuildInterrupted({ ...base, manifest: manifestOf([{ ...draft, origin: undefined }]) })).toBe(true);
    });

    test("an approved manifest with no components has nothing to resume", () => {
        expect(isApprovedBuildInterrupted({ ...base, manifest: { name: "build" } })).toBe(false);
    });

    test("an unfinished component with no status counts as unfinished", () => {
        expect(isApprovedBuildInterrupted({ ...base, manifest: manifestOf([{ ...draft, status: undefined }]) })).toBe(true);
    });

    test("a platform component that reads as a draft does not keep a finished build interrupted", () => {
        const manifest = manifestOf([
            { ...draft, status: "validated" },
            { ...draft, name: "P", filename: "p.yaml", origin: "platform" },
        ]);

        expect(isApprovedBuildInterrupted({ ...base, manifest })).toBe(false);
    });
});

describe("resumeSurface", () => {
    test.each([
        [true, false, "plan-card"],
        [false, false, "timeline"],
        [true, true, "none"],
        [false, true, "none"],
    ])("host verdict %s, cancelling %s: %s", (hostInterrupted, isCancelling, surface) => {
        expect(resumeSurface({ hostInterrupted, isCancelling })).toBe(surface);
    });
});

const steps = [{ type: "status" as const, text: "Generating...", timestamp: 1 }];
const moreSteps = [{ type: "status" as const, text: "Saving...", timestamp: 2 }];
const msg = (over: Record<string, unknown>) => ({ isUser: false, ...over }) as unknown as MessageFE;
const withId = (id: string, over: Record<string, unknown> = {}) => msg({ metadata: { messageId: id }, ...over });
const interrupted = { isInterrupted: true, isTimelineCollapsed: false };

describe("borrowedStepsSource", () => {
    test("an error message with no steps borrows from the last message of the same task that has some", () => {
        const early = withId("early", { taskId: "t", progressUpdates: steps });
        const late = withId("late", { taskId: "t", progressUpdates: moreSteps });
        const error = withId("error", { taskId: "t", isError: true });

        expect(borrowedStepsSource([early, late, error], interrupted)).toBe(late);
    });

    test("borrows nothing when the last message has steps of its own, even if an earlier message of the task has some", () => {
        const early = withId("early", { taskId: "t", progressUpdates: steps });
        const own = withId("own", { taskId: "t", progressUpdates: moreSteps });

        expect(borrowedStepsSource([early, own], interrupted)).toBeUndefined();
    });

    test("does not borrow across tasks", () => {
        const plan = withId("plan", { taskId: "other", progressUpdates: steps });
        const error = withId("error", { taskId: "t", isError: true });

        expect(borrowedStepsSource([plan, error], interrupted)).toBeUndefined();
    });

    test("does not borrow when the last message has no task id, not even from another message without one", () => {
        const work = withId("work", { taskId: "t", progressUpdates: steps });
        const stray = withId("stray", { progressUpdates: steps });
        const bare = withId("bare");

        expect(borrowedStepsSource([work, stray, bare], interrupted)).toBeUndefined();
    });

    test("never borrows from a user message, and a user message after the error does not become the last message", () => {
        const user = { isUser: true, taskId: "t", progressUpdates: steps, metadata: { messageId: "u" } } as unknown as MessageFE;
        const work = withId("work", { taskId: "t", progressUpdates: steps });
        const error = withId("error", { taskId: "t", isError: true });

        expect(borrowedStepsSource([user, error], interrupted)).toBeUndefined();
        expect(borrowedStepsSource([work, error, user], interrupted)).toBe(work);
    });

    test("does not borrow from a message that has no id, since it could not be hidden", () => {
        const noId = msg({ taskId: "t", progressUpdates: steps });
        const error = withId("error", { taskId: "t", isError: true });

        expect(borrowedStepsSource([noId, error], interrupted)).toBeUndefined();
    });

    test.each([
        ["the build is not interrupted", { isInterrupted: false }],
        ["the timeline is collapsed onto its anchor", { isTimelineCollapsed: true }],
    ])("borrows nothing when %s", (_name, over) => {
        const work = withId("work", { taskId: "t", progressUpdates: steps });
        const error = withId("error", { taskId: "t", isError: true });

        expect(borrowedStepsSource([work, error], { ...interrupted, ...over })).toBeUndefined();
    });

    test("borrows nothing when there are no messages", () => {
        expect(borrowedStepsSource([], interrupted)).toBeUndefined();
    });
});

describe("activitySuppressionFor", () => {
    test("hides the message the steps are borrowed from in place of the base choice", () => {
        expect(activitySuppressionFor("error", withId("work"))).toBe("work");
    });

    test("keeps the base choice when nothing is borrowed", () => {
        expect(activitySuppressionFor("error", undefined)).toBe("error");
        expect(activitySuppressionFor(undefined, undefined)).toBeUndefined();
    });
});
