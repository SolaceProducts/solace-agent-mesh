// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { Gateway } from "@/types/models";

import { isConsumeOnlyEventMeshEntrypoint } from "./isConsumeOnlyEventMeshEntrypoint";

const build = (values: Record<string, unknown>): Gateway => ({ name: "e", description: "", type: "event_mesh", values });

describe("isConsumeOnlyEventMeshEntrypoint", () => {
    it("returns false for null / undefined", () => {
        expect(isConsumeOnlyEventMeshEntrypoint(null)).toBe(false);
        expect(isConsumeOnlyEventMeshEntrypoint(undefined)).toBe(false);
    });

    it("returns false when event_rules is missing", () => {
        expect(isConsumeOnlyEventMeshEntrypoint(build({}))).toBe(false);
    });

    it("returns false when event_rules is empty", () => {
        expect(isConsumeOnlyEventMeshEntrypoint(build({ event_rules: [] }))).toBe(false);
    });

    it("returns false when no rule has existingQueueName set", () => {
        const gateway = build({ event_rules: [{ name: "r1", subscriptions: [{ topic: "a/b" }] }] });
        expect(isConsumeOnlyEventMeshEntrypoint(gateway)).toBe(false);
    });

    it("returns false when existingQueueName is an empty / whitespace string", () => {
        expect(isConsumeOnlyEventMeshEntrypoint(build({ event_rules: [{ name: "r1", existingQueueName: "" }] }))).toBe(false);
        expect(isConsumeOnlyEventMeshEntrypoint(build({ event_rules: [{ name: "r1", existingQueueName: "   " }] }))).toBe(false);
    });

    it("returns true when any rule has existingQueueName set", () => {
        const mixed = build({
            event_rules: [
                { name: "normal", subscriptions: [{ topic: "a/b" }] },
                { name: "consume", existingQueueName: "preprov/q/orders" },
            ],
        });
        expect(isConsumeOnlyEventMeshEntrypoint(mixed)).toBe(true);
    });

    it("returns false when event_rules is not an array (defensive)", () => {
        expect(isConsumeOnlyEventMeshEntrypoint(build({ event_rules: "not-an-array" }))).toBe(false);
    });
});
