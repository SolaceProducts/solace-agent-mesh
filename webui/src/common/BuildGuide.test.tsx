// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { ToolsetBuildTarget } from "@/api/toolsets";

import { useResolvedBuildTarget } from "./BuildGuide";

let buildTarget: { data?: ToolsetBuildTarget; isLoading: boolean; isError: boolean };

vi.mock("@/api/toolsets", () => ({
    useToolsetBuildTarget: () => buildTarget,
}));

describe("useResolvedBuildTarget", () => {
    test("uses the target an STR reported", () => {
        buildTarget = { data: { os: "linux", arch: "amd64", source: "str-init", pythonVersion: "3.11" }, isLoading: false, isError: false };

        const { result } = renderHook(() => useResolvedBuildTarget());

        expect(result.current).toEqual({ targetOS: "linux", targetArch: "amd64", targetPython: "3.11", targetLabel: "linux / amd64 · Python 3.11", isError: false });
    });

    test("does not present the fallback as detected", () => {
        buildTarget = { data: { os: "linux", arch: "arm64", source: "fallback" }, isLoading: false, isError: false };

        const { result } = renderHook(() => useResolvedBuildTarget());

        expect(result.current).toEqual({ targetOS: "<os>", targetArch: "<arch>", targetPython: "", targetLabel: "could not detect", isError: true });
    });
});
