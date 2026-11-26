// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { BuildOperationResult } from "@/api/builder/applyTypes";

import { buildOperationError } from "./useSaveOrchestration";

const failed = (name: string, message: string, extra: Partial<BuildOperationResult["error"]> = {}): BuildOperationResult =>
    ({
        kind: "agent",
        name,
        action: "create",
        status: "failed",
        error: { message, ...extra },
    }) as BuildOperationResult;

describe("buildOperationError", () => {
    test("carries the per-field detail the envelope exists to deliver", () => {
        const err = buildOperationError(failed("Agent", "Validation failed.", { validationDetails: { systemPrompt: ["too short", "no template"] } }));
        expect(err.message).toContain("systemPrompt: too short; no template");
    });

    test("names an entrypoint 'entrypoint' when the server sent no message", () => {
        const err = buildOperationError({ kind: "gateway", name: "order_events_entrypoint", action: "create", status: "failed" } as BuildOperationResult);
        expect(err.message).toContain('Failed to save entrypoint "order_events_entrypoint".');
        expect(err.message).not.toMatch(/gateway/i);
    });

    test("names every failure, not only the first", () => {
        const err = buildOperationError(failed("First", "first broke"), [failed("Second", "second broke")]);
        expect(err.message).toContain("first broke");
        expect(err.message).toContain("Second");
        expect(err.message).toContain("second broke");
    });

    test("says what was never attempted", () => {
        const err = buildOperationError(failed("First", "first broke"), [], ["Third", "Fourth"]);
        expect(err.message).toContain("Not attempted: Third, Fourth.");
    });

    test("keeps warnings that would otherwise be lost to the failure", () => {
        const err = buildOperationError(failed("First", "first broke"), [], [], ["Other: kept the stored description"]);
        expect(err.message).toContain("kept the stored description");
    });

    test("still routes the auto-fix classifier off the server's status", () => {
        const err = buildOperationError(failed("Agent", "nope", { httpStatus: 422, errorId: "abc" }));
        expect((err as Error & { status?: number }).status).toBe(422);
        expect((err as Error & { errorId?: string }).errorId).toBe("abc");
    });

    test("falls back to 500 when the server named no status", () => {
        expect((buildOperationError(failed("Agent", "nope")) as Error & { status?: number }).status).toBe(500);
    });
});
