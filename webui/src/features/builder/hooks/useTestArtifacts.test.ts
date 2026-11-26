// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { useTestPlan } from "./useTestArtifacts";

const useChatArtifacts = vi.fn();
const useSessionArtifactContent = vi.fn();
const useChatContext = vi.fn();

vi.mock("@/api/artifacts/hooks", () => ({
    useChatArtifacts: () => useChatArtifacts(),
    useSessionArtifactContent: (...args: unknown[]) => useSessionArtifactContent(...args),
}));
vi.mock("@/providers/contexts", () => ({
    useChatContext: () => useChatContext(),
}));

const b64 = (s: string) => btoa(unescape(encodeURIComponent(s)));

describe("useTestPlan (useArtifactJSON list source)", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        useChatContext.mockReturnValue({ sessionId: "sess-1" });
    });

    test("parses the plan when test_plan.json is present in useChatArtifacts()", () => {
        useChatArtifacts.mockReturnValue({ data: [{ filename: "test_plan.json", version: 0 }] });
        useSessionArtifactContent.mockReturnValue({ data: { content: b64(JSON.stringify({ componentName: "OrderValidator" })) }, isPending: false });

        const { result } = renderHook(() => useTestPlan());

        expect(useSessionArtifactContent).toHaveBeenCalledWith("sess-1", "test_plan.json", 0);
        expect(result.current.plan).toEqual({ componentName: "OrderValidator" });
        expect(result.current.runNumber).toBe(1);
    });

    test("returns null when the artifact list is empty (regression guard: list must come from useChatArtifacts, which can be empty)", () => {
        useChatArtifacts.mockReturnValue({ data: [] });
        useSessionArtifactContent.mockReturnValue({ data: undefined, isPending: false });

        const { result } = renderHook(() => useTestPlan());

        expect(useSessionArtifactContent).toHaveBeenCalledWith("sess-1", null, undefined);
        expect(result.current.plan).toBeNull();
        expect(result.current.runNumber).toBeNull();
    });
});
