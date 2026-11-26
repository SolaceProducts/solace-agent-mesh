// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

type MockCard = { name: string; displayName?: string; isWorkflow?: boolean; isEphemeral?: boolean };

describe("useWorkflowOptions", () => {
    let useWorkflowOptions: typeof import("@/features/entrypoints/hooks/useWorkflowOptions").useWorkflowOptions;
    let pinWorkflowValue: typeof import("@/features/entrypoints/hooks/useWorkflowOptions").pinWorkflowValue;
    let mockCards: MockCard[] | undefined | null;
    let mockIsError: boolean;

    beforeEach(async () => {
        vi.resetModules();
        mockCards = [];
        mockIsError = false;
        vi.doMock("@/api/agent-cards", () => ({
            useAgentCards: () => ({ data: mockCards, isError: mockIsError }),
        }));
        const mod = await import("@/features/entrypoints/hooks/useWorkflowOptions");
        useWorkflowOptions = mod.useWorkflowOptions;
        pinWorkflowValue = mod.pinWorkflowValue;
    });

    test("returns empty options while loading (undefined data)", () => {
        mockCards = undefined;
        const { result } = renderHook(() => useWorkflowOptions());
        expect(result.current.options).toEqual([]);
    });

    test("does not crash on a null data payload", () => {
        mockCards = null;
        const { result } = renderHook(() => useWorkflowOptions());
        expect(result.current.options).toEqual([]);
    });

    test("keeps workflow cards only, dropping agents and ephemeral wrappers", () => {
        mockCards = [
            { name: "Orchestrator", displayName: "Orchestrator" },
            { name: "OrderIntake", displayName: "Order Intake", isWorkflow: true },
            { name: "TestWrapper", displayName: "Test Wrapper", isWorkflow: true, isEphemeral: true },
        ];
        const { result } = renderHook(() => useWorkflowOptions());
        expect(result.current.options).toEqual([{ name: "OrderIntake", displayName: "Order Intake" }]);
    });

    test("falls back to the broker name when a card has no display name", () => {
        mockCards = [{ name: "workflow_019f28bd", isWorkflow: true }];
        const { result } = renderHook(() => useWorkflowOptions());
        expect(result.current.options).toEqual([{ name: "workflow_019f28bd", displayName: "workflow_019f28bd" }]);
    });

    test("sorts by display name", () => {
        mockCards = [
            { name: "c", displayName: "Zeta", isWorkflow: true },
            { name: "a", displayName: "Alpha", isWorkflow: true },
            { name: "b", displayName: "Mu", isWorkflow: true },
        ];
        const { result } = renderHook(() => useWorkflowOptions());
        expect(result.current.options.map(o => o.displayName)).toEqual(["Alpha", "Mu", "Zeta"]);
    });

    test("surfaces the fetch-error flag so the picker can explain an empty list", () => {
        mockCards = [];
        mockIsError = true;
        const { result } = renderHook(() => useWorkflowOptions());
        expect(result.current.options).toEqual([]);
        expect(result.current.isError).toBe(true);
    });

    test("reports no error on a healthy empty list", () => {
        const { result } = renderHook(() => useWorkflowOptions());
        expect(result.current.isError).toBe(false);
    });

    describe("pinWorkflowValue", () => {
        const options = [{ name: "OrderIntake", displayName: "Order Intake" }];

        test("appends a configured workflow that isn't in the fetched list (undeployed / renamed)", () => {
            expect(pinWorkflowValue(options, "RetiredWorkflow")).toEqual([
                { name: "OrderIntake", displayName: "Order Intake" },
                { name: "RetiredWorkflow", displayName: "RetiredWorkflow" },
            ]);
        });

        test("matches on the broker name, not the display name", () => {
            expect(pinWorkflowValue(options, "Order Intake")).toEqual([
                { name: "OrderIntake", displayName: "Order Intake" },
                { name: "Order Intake", displayName: "Order Intake" },
            ]);
        });

        test("leaves the list unchanged when the value already exists", () => {
            expect(pinWorkflowValue(options, "OrderIntake")).toBe(options);
        });

        test("leaves a blank value alone (placeholder handles 'nothing selected')", () => {
            expect(pinWorkflowValue(options, "")).toBe(options);
            expect(pinWorkflowValue(options, undefined)).toBe(options);
        });
    });
});
