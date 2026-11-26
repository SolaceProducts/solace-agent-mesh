// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { ApplyBuildResponse, BuildOperationResult } from "@/api/builder/applyTypes";
import type { ApiError } from "@/api/client";

import { classifyDryRun, classifyDryRunError, PLATFORM_CHECK_CHIP, platformCheckMessage } from "./platformCheck";

function op(overrides: Partial<BuildOperationResult> = {}): BuildOperationResult {
    return { kind: "agent", name: "Triage", action: "create", status: "planned", ...overrides };
}

function failed(name: string, httpStatus: number, message = `${name} refused`, kind: BuildOperationResult["kind"] = "agent"): BuildOperationResult {
    return op({ kind, name, status: "failed", error: { message, errorId: "e", httpStatus } });
}

function response(operations: BuildOperationResult[]): ApplyBuildResponse {
    return { dryRun: true, operations, summary: { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 }, dropped: [] };
}

function apiError(message: string, status?: number): ApiError {
    const error = new Error(message) as ApiError;
    if (status !== undefined) error.status = status;
    return error;
}

describe("classifyDryRun", () => {
    it("passes a plan with every operation planned, carrying the warnings", () => {
        const check = classifyDryRun(response([op({ name: "A", warnings: ["w1"] }), op({ name: "B", warnings: ["w2"] })]));
        expect(check).toEqual({ kind: "passed", warnings: ["A: w1", "B: w2"] });
    });

    it("refuses a plan with a 422 failure and names the component", () => {
        const check = classifyDryRun(response([op({ name: "A" }), failed("B", 422, "bad model", "workflow")]));
        expect(check).toEqual({ kind: "refused", problems: [{ kind: "workflow", name: "B", message: "bad model" }], forbidden: [] });
    });

    it("refuses on 400 and 409 as well", () => {
        const check = classifyDryRun(response([failed("A", 400), failed("B", 409)]));
        expect(check).toMatchObject({ kind: "refused", problems: [{ name: "A" }, { name: "B" }] });
    });

    it("lists a 403 apart from the refusals", () => {
        const check = classifyDryRun(response([failed("A", 422), failed("B", 403, "no scope")]));
        expect(check).toEqual({
            kind: "refused",
            problems: [{ kind: "agent", name: "A", message: "A refused" }],
            forbidden: [{ kind: "agent", name: "B", message: "no scope" }],
        });
    });

    it("says not allowed when every failure is a 403", () => {
        const check = classifyDryRun(response([op(), failed("B", 403, "no scope")]));
        expect(check).toEqual({ kind: "notAllowed", problems: [{ kind: "agent", name: "B", message: "no scope" }] });
    });

    it("could not answer when the only failures are not refusals", () => {
        const check = classifyDryRun(response([failed("A", 500, "db down")]));
        expect(check.kind).toBe("unavailable");
    });

    it("still refuses when a refusal sits beside a failure it cannot read", () => {
        const check = classifyDryRun(response([failed("A", 500), failed("B", 422)]));
        expect(check).toMatchObject({ kind: "refused", problems: [{ name: "B" }] });
    });
});

describe("classifyDryRunError", () => {
    it("says not allowed on a thrown 403", () => {
        const check = classifyDryRunError(apiError("forbidden", 403));
        expect(check).toEqual({ kind: "notAllowed", problems: [{ kind: null, name: null, message: "forbidden" }] });
    });

    it.each([400, 409, 422])("refuses the whole request on a thrown %i", status => {
        const check = classifyDryRunError(apiError("manifest rejected", status));
        expect(check).toEqual({ kind: "refused", problems: [{ kind: null, name: null, message: "manifest rejected" }], forbidden: [] });
    });

    it.each([undefined, 500, 502, 401])("could not answer on status %s", status => {
        const check = classifyDryRunError(apiError("boom", status));
        expect(check).toEqual({ kind: "unavailable", message: "boom" });
    });

    it("could not answer on a non-error throw", () => {
        expect(classifyDryRunError("nope").kind).toBe("unavailable");
    });
});

describe("platformCheckMessage", () => {
    it("lists one bullet per problem and asks for a fix and a full validation", () => {
        const message = platformCheckMessage([
            { kind: "agent", name: "Triage", message: "unknown model" },
            { kind: "workflow", name: "Flow", message: "no such agent" },
        ]);
        expect(message.startsWith("[automated] Platform check")).toBe(true);
        expect(message).toContain("- agent Triage: unknown model\n- workflow Flow: no such agent");
        expect(message).toContain("validate_build_manifest");
        expect(message).toContain("full_validation: true");
    });

    it("words a whole-request problem without a component", () => {
        expect(platformCheckMessage([{ kind: null, name: null, message: "manifest rejected" }])).toContain("- manifest rejected");
    });
});

describe("PLATFORM_CHECK_CHIP", () => {
    it("is the auto-fix chip's style around the platform check text", () => {
        expect(PLATFORM_CHECK_CHIP).toBe('<span style="display:inline-flex;align-items:center;gap:4px;padding:4px 12px;border-radius:6px;font-size:13px;font-weight:500;opacity:0.7">⟳ Platform check</span>');
    });
});
