// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { actionLabel, deployFailureMessage } from "./messaging";

describe("actionLabel", () => {
    test.each([
        ["deploy", "noun", "Deployment"],
        ["undeploy", "noun", "Undeployment"],
        ["update", "noun", "Update"],
        ["deploy", "past", "Deployed"],
        ["undeploy", "past", "Undeployed"],
        ["update", "past", "Updated"],
    ] as const)("(%s, %s) → %s", (action, tense, expected) => {
        expect(actionLabel(action, tense)).toBe(expected);
    });

    test("defaults to noun form when tense is omitted", () => {
        expect(actionLabel("deploy")).toBe("Deployment");
    });
});

describe("deployFailureMessage", () => {
    test("null response returns null (treat as success/in-flight)", () => {
        expect(deployFailureMessage(null, "Deployment")).toBeNull();
        expect(deployFailureMessage(undefined, "Deployment")).toBeNull();
    });

    test("status absent returns null (legacy callers without status)", () => {
        expect(deployFailureMessage({}, "Deployment")).toBeNull();
    });

    test("status:'success' returns null", () => {
        expect(deployFailureMessage({ status: "success" }, "Deployment")).toBeNull();
    });

    test("status:'failed' with errorMessage returns the message", () => {
        expect(deployFailureMessage({ status: "failed", errorMessage: "AWE timed out" }, "Deployment")).toBe("AWE timed out");
    });

    test("status:'failed' without errorMessage returns a generic message", () => {
        expect(deployFailureMessage({ status: "failed" }, "Deployment")).toBe("Deployment reported status: failed");
    });

    test("unknown non-success status returns a generic message", () => {
        expect(deployFailureMessage({ status: "in_progress" as any }, "Update")).toBe("Update reported status: in_progress");
    });
});
