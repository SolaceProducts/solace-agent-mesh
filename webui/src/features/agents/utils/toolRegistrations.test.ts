// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { ToolRegistration } from "@/types";

import { runtimeToolNamesForConnector } from "./toolRegistrations";

const reg = (overrides: Partial<ToolRegistration>): ToolRegistration => ({ toolType: "openapi", status: "ready", toolCount: 0, ...overrides });

describe("runtimeToolNamesForConnector", () => {
    const registrations: ToolRegistration[] = [
        reg({ connectorId: "conn-1", status: "ready", toolNames: ["a", "b"], toolCount: 2 }),
        reg({ connectorId: "conn-2", status: "failed", error: "boom" }),
        reg({ connectorId: "conn-3", status: "ready", toolNames: [], toolCount: 0 }),
    ];

    test("returns the names for a ready connector entry", () => {
        expect(runtimeToolNamesForConnector(registrations, "conn-1")).toEqual(["a", "b"]);
    });

    test("returns undefined for a failed entry", () => {
        expect(runtimeToolNamesForConnector(registrations, "conn-2")).toBeUndefined();
    });

    test("returns undefined for a ready entry with no names", () => {
        expect(runtimeToolNamesForConnector(registrations, "conn-3")).toBeUndefined();
    });

    test("returns undefined when unmatched or inputs missing", () => {
        expect(runtimeToolNamesForConnector(registrations, "nope")).toBeUndefined();
        expect(runtimeToolNamesForConnector(undefined, "conn-1")).toBeUndefined();
        expect(runtimeToolNamesForConnector(registrations, undefined)).toBeUndefined();
    });
});
